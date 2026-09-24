package app

import (
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"screenguard/internal/capture"
	"screenguard/internal/click"
	"screenguard/internal/engine"
	"screenguard/internal/events"
	"screenguard/internal/infer"
	"screenguard/internal/paths"
	"screenguard/internal/platform"
	"screenguard/internal/policy"
	"screenguard/internal/selftest"
	"screenguard/internal/storage"
	"screenguard/internal/types"
)

// ============================================================
// 应用主控 — 帧调度、推理编排、事件落盘
// PRD 4.3.6 帧调度与背压 + 进程模型
// ============================================================

// App 主应用结构
type App struct {
	mu sync.Mutex

	// 核心组件
	config    *types.AppConfig
	db        *storage.DB
	jsonl     *events.JSONLWriter
	eng       *engine.Engine
	policyMgr *policy.Manager
	detector  infer.Detector
	capturer  capture.Capturer
	clicker   click.Clicker

	// 帧调度（队列携带抓屏耗时，供心跳与帧推送使用）
	frameQueue    chan capturedFrame
	droppedCount  int
	actualFps     float64
	lastCapture   time.Time
	lastCaptureMs int
	prevFrame     image.Image

	// 突发模式（Burst）：屏幕上存在弹窗时的加速采集
	//
	// 背景：帧差门控（diff<1% 跳过推理）在弹窗静止时会让推理彻底停摆——
	// 实测 10 分钟只推理 54 帧（≈0.09fps，配置是 2.5fps），相邻推理间隔
	// 中位 1.53s、最大 12.9s。而 ✕ 按钮只有约 41% 的帧能检出，
	// 于是"弹窗出现 → 5~7 秒才点到"。
	//
	// 对策：一旦检出弹窗容器，就在一段时间内跳过帧差门控并提高抓屏帧率，
	// 让 ✕ 漏检能在下一帧立刻补上，而不是等下一次屏幕变化。
	burstMu    sync.Mutex
	burstUntil time.Time // 高速突发截止（搜捕 ✕）
	watchUntil time.Time // 低速监视截止（仅跳过帧差门控）
	burstFired bool      // 本次弹窗是否已执行过点击（点击后降速，避免空转烧 CPU）

	// popupFirstSeen 本轮弹窗首次检出的时刻，用于观测"发现 → 点击"的真实端到端延迟
	popupFirstSeen time.Time

	// 帧推送（调试台画布）
	frames *FramePusher

	// 最近一次点击记录（供"刚才那个不该点"撤销，PRD 3.9）
	lastClickMu   sync.Mutex
	lastClickAt   time.Time
	lastClickDone bool

	// 禁区检测（PRD 4.3.1 第 1 步）
	// macOS 有 TCC 授权做兜底，Windows 没有 TCC，禁区判定是唯一的安全边界，
	// 必须每帧/每秒刷新并下发给引擎，否则会在全屏游戏 / 锁屏 / 远程桌面里误点。
	forbiddenFunc   func() (bool, string)
	forbiddenIn     bool
	forbiddenReason string

	// 状态
	running bool
	stopCh  chan struct{}

	// 配置文件路径（用于 GetConfig/SaveConfig 持久化）
	configPath string
}

// capturedFrame 一帧抓屏结果
type capturedFrame struct {
	img       image.Image
	captureMs int
	ts        time.Time
}

// 突发模式参数
const (
	// burstFps 弹窗在场且尚未点击时的抓屏帧率（搜捕 ✕）。
	// 常规 2.5fps 下 ✕ 漏检要等下一次屏幕变化才补得上，实测延迟 5~7 秒。
	burstFps = 8.0
	// watchFps 已点过一次后的监视帧率（等抑制窗口 / 确认弹窗是否关闭）。
	// 此时再高速跑没有意义：点击预算与 suppress_rapid_ms 都会拦住。
	watchFps = 3.0
	// burstHold 检到弹窗后维持高速突发的时长（每检出一次就续期）
	burstHold = 5 * time.Second
	// watchHold 检到弹窗后维持"跳过帧差门控"的时长（比突发更久，保证能重试）
	watchHold = 20 * time.Second
)

// markPopupSeen 记录"这一帧屏幕上存在弹窗"，据此进入/续期突发模式。
// acted=true 表示本次弹窗已经点过，降为低速监视，避免长时间空转烧 CPU。
func (a *App) markPopupSeen(acted bool) {
	now := time.Now()
	a.burstMu.Lock()
	defer a.burstMu.Unlock()
	if acted {
		// 已点击过：不再高速搜捕，只保留"跳过帧差门控"的监视
		a.burstUntil = time.Time{}
		a.watchUntil = now.Add(watchHold)
		a.burstFired = true
		return
	}
	if !a.burstFired {
		a.burstUntil = now.Add(burstHold)
	}
	a.watchUntil = now.Add(watchHold)
	if a.popupFirstSeen.IsZero() {
		a.popupFirstSeen = now
	}
}

// markPopupGone 记录"这一帧没有弹窗"，允许下次弹窗重新进入高速突发。
func (a *App) markPopupGone() {
	a.burstMu.Lock()
	a.burstFired = false
	a.popupFirstSeen = time.Time{}
	a.burstMu.Unlock()
}

// popupAge 返回本轮弹窗从首次检出到现在的时长（"发现 → 点击"端到端延迟观测用）
func (a *App) popupAge() time.Duration {
	a.burstMu.Lock()
	defer a.burstMu.Unlock()
	if a.popupFirstSeen.IsZero() {
		return 0
	}
	return time.Since(a.popupFirstSeen)
}

// burstRemaining 返回高速突发的剩余时长（<=0 表示不在突发态）
func (a *App) burstRemaining() time.Duration {
	a.burstMu.Lock()
	defer a.burstMu.Unlock()
	return time.Until(a.burstUntil)
}

// watchRemaining 返回监视态的剩余时长（>0 时应跳过帧差门控）
func (a *App) watchRemaining() time.Duration {
	a.burstMu.Lock()
	defer a.burstMu.Unlock()
	return time.Until(a.watchUntil)
}

// New 创建应用实例
func New(cfg *types.AppConfig, dbPath, jsonlDir string) (*App, error) {
	// 崩溃日志与运行日志同级（jsonlDir 即 <logDir>/events，取上级即软件目录/log），
	// 满足"所有日志都在软件目录"的要求；GUI 子系统下 stderr 不可见，必须落盘取证。
	SetCrashLogPath(filepath.Join(filepath.Dir(jsonlDir), "crash.log"))

	// 打开数据库
	db, err := storage.New(dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}

	// 打开 JSONL 写入器
	jsonl, err := events.NewJSONLWriter(jsonlDir)
	if err != nil {
		return nil, fmt.Errorf("打开事件流: %w", err)
	}

	// 创建策略管理器
	pm := policy.NewManager(cfg.PolicyPath)

	// 加载或创建默认策略
	if _, err := pm.LoadAndApply(); err != nil {
		log.Printf("[WARN] 策略加载失败，使用默认策略: %v", err)
		policy.WriteDefault(cfg.PolicyPath)
		snap, _ := pm.LoadAndApply()
		_ = snap
	}

	// 创建决策引擎
	eng := engine.NewEngine(cfg)
	snap := pm.Current()
	if snap != nil {
		eng.SetPolicy(snap)
	}

	// 帧队列容量 1~2（PRD 4.3.6）
	queueCap := 2
	if cfg.PerformanceTier == types.PerfPowersave {
		queueCap = 1
	}

	return &App{
		config:     cfg,
		db:         db,
		jsonl:      jsonl,
		eng:        eng,
		policyMgr:  pm,
		frameQueue: make(chan capturedFrame, queueCap),
		stopCh:     make(chan struct{}),
		frames:     NewFramePusher(),
	}, nil
}

// SetDetector 设置推理器
func (a *App) SetDetector(d infer.Detector) { a.detector = d }

// SetCapturer 设置抓屏器
func (a *App) SetCapturer(c capture.Capturer) { a.capturer = c }

// ExcludeSelfWindows 自激防护（PRD 4.3.7）：排除当前进程全部顶层窗口，
// 使主窗口/调试台/探针不被自身抓屏拍到。需在窗口创建后调用。
func (a *App) ExcludeSelfWindows() error {
	if a.capturer == nil {
		return nil
	}
	return a.capturer.ExcludeSelfWindows()
}

// SetClicker 设置点击器
func (a *App) SetClicker(c click.Clicker) { a.clicker = c }

// IsScreenCaptureAuthorized 检查屏幕录制权限（API 层）
func (a *App) IsScreenCaptureAuthorized() bool {
	return a.capturer != nil && a.capturer.IsAuthorized()
}

// ScreenPermissionState 屏幕权限三态（红线 A2/A4）
//
//	"unauthorized"      系统层面未授权
//	"needs_restart"     已授权但本进程未生效（授权后需重启，PRD 3.1.2）
//	"ok"                授权且抓屏正常
func (a *App) ScreenPermissionState() string {
	if !a.IsScreenCaptureAuthorized() {
		return "unauthorized"
	}
	// 已授权：用实际抓屏验证是否生效。
	// 注意（修复"无法识别权限"）：ScreenCaptureKit 在【未授权】时返回的是
	// 错误，而不是壁纸；【已授权】时即使桌面是纯色壁纸也会成功返回图像。
	// 因此"抓屏成功"就是授权的充分证据。之前的像素方差门控会把纯色壁纸
	// 误判成"未生效"，导致授权了仍显示 needs_restart —— 已移除。
	img, _, err := a.capturer.Capture()
	if err != nil || img == nil {
		return "needs_restart"
	}
	return "ok"
}

// RestartApp 以正确的 bundle 身份重新拉起自己，然后退出当前进程。
// 授权后 macOS 只对"重启后的进程"生效；用 open -n 重启 .app 能保证
// 重新拉起的是带 entitlements 的 ScreenGuard.app（身份 com.screenguard.app），
// 而不是用户误点的裸二进制。
func (a *App) RestartApp() {
	go func() {
		time.Sleep(200 * time.Millisecond)
		if bundle := paths.BundlePath(); bundle != "" {
			if cmd := exec.Command("open", "-n", bundle); cmd.Start() == nil {
				time.Sleep(400 * time.Millisecond)
			}
		}
		os.Exit(0)
	}()
}

// probeFrameStd 采样计算帧灰度标准差
func probeFrameStd(img image.Image) float64 {
	if img == nil {
		return -1
	}
	b := img.Bounds()
	step := 24
	var sum, sumSq float64
	var n int
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			r, g, bl, _ := img.At(x, y).RGBA()
			gray := 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(bl>>8)
			sum += gray
			sumSq += gray * gray
			n++
		}
	}
	if n == 0 {
		return -1
	}
	mean := sum / float64(n)
	v := sumSq/float64(n) - mean*mean
	if v < 0 {
		v = 0
	}
	return v
}

// IsAccessibilityAuthorized 检查辅助功能权限
func (a *App) IsAccessibilityAuthorized() bool {
	type authorizer interface{ IsAuthorized() bool }
	c, ok := a.clicker.(authorizer)
	return ok && c.IsAuthorized()
}

// RequestScreenCapturePermission 请求屏幕录制权限（只在用户主动点击时触发）
func (a *App) RequestScreenCapturePermission() bool {
	type requester interface{ RequestPermission() bool }
	c, ok := a.capturer.(requester)
	return ok && c.RequestPermission()
}

// RequestAccessibilityPermission 请求辅助功能权限（只在用户主动点击时触发）
func (a *App) RequestAccessibilityPermission() bool {
	type requester interface{ RequestPermission() bool }
	c, ok := a.clicker.(requester)
	return ok && c.RequestPermission()
}

// PermissionStatus 返回当前权限状态（含三态 screen_state，红线 A2）
func (a *App) PermissionStatus() map[string]any {
	return map[string]any{
		"screen":        a.IsScreenCaptureAuthorized(),
		"accessibility": a.IsAccessibilityAuthorized(),
		"screen_state":  a.ScreenPermissionState(),
	}
}

// Start 启动帧调度循环
func (a *App) Start() {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return
	}
	a.running = true
	a.mu.Unlock()

	// 抓屏 goroutine
	go a.captureLoop()

	// 推理 goroutine
	go a.inferLoop()

	// 策略热更新 goroutine（定时拉取，默认 10 分钟）
	go a.policyPollLoop()

	// 禁区轮询 goroutine（1 秒）
	go a.forbiddenPollLoop()

	log.Println("[App] 帧调度循环已启动")
}

// Stop 停止
func (a *App) Stop() {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return
	}
	a.running = false
	close(a.stopCh)
	a.mu.Unlock()
}

// captureLoop 抓屏循环 — 定时驱动（2.5~3 fps）
func (a *App) captureLoop() {
	normalInterval := a.frameInterval(a.config.FpsLimit)
	burstInterval := a.frameInterval(burstFps)
	watchInterval := a.frameInterval(watchFps)

	ticker := time.NewTicker(normalInterval)
	defer ticker.Stop()
	cur := normalInterval

	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			// 单次抓屏 panic 隔离：一次异常不应拖垮整个采集协程
			//（否则表现为"软件静默卡死"，且 GUI 下看不到任何报错）
			func() {
				defer RecoverPanic("captureLoop")
				if a.capturer == nil || !a.capturer.IsAuthorized() {
					return
				}
				// 弹窗在场 → 跳过帧差门控。
				// 帧差门控的本意是省 CPU，但弹窗静止时会让推理彻底停摆，
				// 而 ✕ 小目标本身就有约 6 成帧漏检，两者叠加就是"5~7 秒才点到"。
				burst := a.burstRemaining() > 0
				watch := a.watchRemaining() > 0

				img, captureMs, err := a.capturer.Capture()
				if err != nil {
					log.Printf("[capture] %v", err)
					return
				}
				a.lastCapture = time.Now()
				a.lastCaptureMs = captureMs

				// 帧差门控 — 无变化区域不推理（突发/监视态下跳过）
				if !burst && !watch && a.prevFrame != nil {
					diff, _ := capture.FrameDiff(a.prevFrame, img, 10)
					if diff < 0.01 { // 变化 < 1% → 跳过
						return
					}
				}
				a.prevFrame = img

				// 有界队列，丢最旧帧
				cf := capturedFrame{img: img, captureMs: captureMs, ts: time.Now()}
				select {
				case a.frameQueue <- cf:
				default:
					a.droppedCount++
					// 丢弃最旧帧
					select {
					case <-a.frameQueue:
					default:
					}
					a.frameQueue <- cf
				}
			}()

			// 按突发状态调整节奏：弹窗在场提速，弹窗消失回落常规帧率
			want := normalInterval
			if a.watchRemaining() > 0 {
				want = watchInterval
			}
			if a.burstRemaining() > 0 {
				want = burstInterval
			}
			if want != cur {
				ticker.Reset(want)
				cur = want
			}
		}
	}
}

// frameInterval 把帧率换算为采集间隔，并做下限保护（防止配置成 0 导致忙循环）
func (a *App) frameInterval(fps float64) time.Duration {
	if fps <= 0 {
		fps = 2.5
	}
	d := time.Duration(float64(time.Second) / fps)
	if d < 40*time.Millisecond {
		d = 40 * time.Millisecond
	}
	return d
}

// inferLoop 推理循环 — 消费帧队列
func (a *App) inferLoop() {
	defer RecoverPanic("inferLoop(顶层)")
	for {
		select {
		case <-a.stopCh:
			return
		case cf := <-a.frameQueue:
			// 单帧 panic 隔离：一帧异常不应终止整个推理循环
			func() {
				defer RecoverPanic("processFrame")
				a.processFrame(cf)
			}()
		}
	}
}

// processFrame 处理单帧
func (a *App) processFrame(cf capturedFrame) {
	img := cf.img
	if a.detector == nil {
		return
	}

	screenW, screenH, scaleFactor, _ := a.capturer.ScreenSize()

	// imgsz 来源链（红线 C9）: 配置覆盖 > profile/基线公式 > 模型静态输入
	// 注意：yolo26m.onnx 输入是动态 [b,3,h,w]，可按基线跑；
	// 若模型为静态输入（如 redetr 的 1024 固定），以模型为准。
	imgsz := a.config.ImgszOverride
	if imgsz == 0 {
		imgsz = infer.ComputeImgszBaseline(screenW)
	}
	if a.detector != nil {
		if m := a.detector.ModelInputSize(); m > 0 && m != imgsz {
			// 静态输入模型：强制对齐模型尺寸，避免 reshape 错误
			imgsz = m
		}
	}

	// 两级检测
	result, err := infer.TwoStageDetect(a.detector, img, screenW, screenH, imgsz,
		a.getConfThresholds(), types.ClassNames)
	if err != nil {
		log.Printf("[infer] %v", err)
		return
	}

	// 合并容器和出口的 box 列表
	var boxes []types.DetectionBox
	boxes = append(boxes, result.Containers...)
	boxes = append(boxes, result.Exits...)

	// 突发模式续期：屏幕上有弹窗 → 跳过帧差门控并提速，让 ✕ 漏检下一帧就能补上
	if len(result.Containers) > 0 {
		a.markPopupSeen(false)
	} else {
		a.markPopupGone()
	}

	// 决策
	totalMs := result.Level1Ms + result.Level2Ms
	event := a.eng.Decide(boxes, screenW, screenH, cf.captureMs, totalMs)

	// 决策观测日志：检测到候选弹窗时，把决策结果与原因实时打到终端，
	// 用户能直接看到"为什么没点/点了什么/被哪个门拦下"
	if event != nil && len(boxes) > 0 {
		target := selectBestBox(boxes)
		name, conf := "无出口", 0.0
		if target != nil {
			name = target.ClSName
			conf = target.Conf
		}
		log.Printf("[Decision] action=%-10s 目标=%-10s 置信=%.0f%% 框数=%d 截屏=%dms 推理=%dms",
			event.Action, name, conf*100, len(boxes), event.CaptureMs, event.ModelMs)
	}

	// 推送给调试台画布（限流/冻结在 FramePusher 内部处理，失败不影响检测）
	if a.frames != nil {
		lb := infer.ComputeLetterbox(screenW, screenH, imgsz)
		var containersOnly, exitsOnly []types.DetectionBox
		if result != nil {
			containersOnly = result.Containers
			exitsOnly = result.Exits
		}
		a.frames.Push(img, lb, imgsz, containersOnly, exitsOnly, event,
			cf.captureMs, result.Level1Ms+result.Level2Ms, screenW, screenH)
	}

	// 落盘
	if event != nil {
		// SQLite
		if _, err := a.db.InsertEvent(event); err != nil {
			log.Printf("[db] 插入事件失败: %v", err)
		}
		// JSONL
		if err := a.jsonl.Append(event); err != nil {
			log.Printf("[jsonl] 追加事件失败: %v", err)
		}

		// 命中事件时存盘截图（受 SaveScreenshots + 路径配置控制，默认 exe 同级 images/）
		a.maybeSaveScreenshot(img, event)

		// 如果决策是点击，执行
		best := selectBestBox(boxes)
		if event.Action == types.ActionClick && a.clicker != nil && best != nil {
			// 全局自动模式，或观察模式下某类别被单独设为"自动点" → 移动光标并真实点击（含消失验证）
			pointPx := [2]float64{best.Center[0], best.Center[1]}
			pointPt := [2]float64{
				infer.PxToPt(best.Center[0], scaleFactor),
				infer.PxToPt(best.Center[1], scaleFactor),
			}

			exec := click.NewExecutor(a.clicker)
			result, retryN, latencyMs := exec.ExecuteClick(pointPx, scaleFactor, img)

			clickAction := &types.ClickAction{
				EventID:      event.ID,
				PointPx:      pointPx,
				PointPt:      pointPt,
				ScaleFactor:  scaleFactor,
				ExecutedAt:   time.Now(),
				VerifyResult: types.Result(result),
				RetryN:       retryN,
				LatencyMs:    latencyMs,
			}
			a.db.InsertClickAction(clickAction)
			a.jsonl.AppendClick(clickAction)

			// 已点击过 → 突发降为低速监视。
			// 继续高速跑没有意义：suppress_rapid_ms 与点击预算都会拦住这段时间内的重复点击，
			// 只保留"跳过帧差门控"以便弹窗没关掉时能在抑制窗口过后重试。
			age := a.popupAge()
			a.markPopupSeen(true)
			if age > 0 {
				log.Printf("[Timing] 发现弹窗 → 执行点击 端到端 %.1fs", age.Seconds())
			}

			// 记录最近一次点击，供"刚才那个不该点"撤销（PRD 3.9）
			a.lastClickMu.Lock()
			a.lastClickAt = clickAction.ExecutedAt
			a.lastClickDone = true
			a.lastClickMu.Unlock()

			// 反馈给引擎
			a.eng.RecordClickResult(best.ClSName, types.Result(result))
		} else if event.Action == types.ActionReportOnly && a.clicker != nil && a.config.MouseFollow {
			// 观察模式：仅把鼠标悬停到检测到的目标位置，不点击。
			// 这样用户能直观看到软件"已经发现"了弹窗/关闭按钮，验证其在生效，
			// 又不会真的误操作。引擎只在用户空闲 >1.5s 时才走到这里，故不会干扰操作。
			// MouseFollow 开关让用户可一键关闭"路径跟随"（例如想自己操作鼠标时）。
			if t := selectHoverTarget(boxes); t != nil {
				a.clicker.MoveTo(t.Center[0], t.Center[1], scaleFactor)
			}
		}
	}
}

// policyPollLoop 策略热更新轮询
func (a *App) policyPollLoop() {
	defer RecoverPanic("policyPollLoop")
	interval := time.Duration(a.config.PolicyPollSec) * time.Second
	if interval < 60*time.Second {
		interval = 10 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			snap, err := a.policyMgr.LoadAndApply()
			if err != nil {
				log.Printf("[policy] 热更新失败，继续使用本地缓存: %v", err)
				continue
			}
			a.eng.SetPolicy(snap)
			log.Printf("[policy] 热更新成功: version=%s", snap.Version)
		}
	}
}

// getConfThresholds 获取当前策略的逐类阈值
func (a *App) getConfThresholds() map[string]float64 {
	snap := a.policyMgr.Current()
	if snap == nil {
		return map[string]float64{
			"TanChuang": 0.30, "GuanBi": 0.25,
		}
	}
	return snap.ConfThresholds
}

// selectBestBox 选最优出口
func selectBestBox(boxes []types.DetectionBox) *types.DetectionBox {
	var best *types.DetectionBox
	for i := range boxes {
		if boxes[i].Role != types.RoleExit {
			continue
		}
		if !boxes[i].GatePassed {
			continue
		}
		if best == nil || boxes[i].Conf > best.Conf {
			b := boxes[i]
			best = &b
		}
	}
	return best
}

// selectHoverTarget 为"鼠标路径跟随"挑选要悬停的框：
// 优先最可信且通过门控的出口，否则退回最可信的容器，保证检测到弹窗时
// 光标能落到可见目标上（而不仅仅是出口）。
func selectHoverTarget(boxes []types.DetectionBox) *types.DetectionBox {
	var exit, container *types.DetectionBox
	for i := range boxes {
		switch boxes[i].Role {
		case types.RoleExit:
			if boxes[i].GatePassed && (exit == nil || boxes[i].Conf > exit.Conf) {
				b := boxes[i]
				exit = &b
			}
		case types.RoleContainer:
			if container == nil || boxes[i].Conf > container.Conf {
				b := boxes[i]
				container = &b
			}
		}
	}
	if exit != nil {
		return exit
	}
	return container
}

// screenshotDir 解析截图保存目录：
// 配置显式指定则用指定路径，否则默认落到 exe 同级 images 目录
// （分发场景下无论程序被拷到哪，截图都随程序走，不污染 CWD）。
func (a *App) screenshotDir() string {
	if a.config.ScreenshotPath != "" {
		return a.config.ScreenshotPath
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "images")
	}
	return "images"
}

// maybeSaveScreenshot 命中事件时把当前帧存盘（降尺度到 1280 宽，避免 4K 原图写爆磁盘），
// 并按 ScreenshotDays 轮转过期截图。受 SaveScreenshots 开关控制。
func (a *App) maybeSaveScreenshot(img image.Image, ev *types.DetectionEvent) {
	if !a.config.SaveScreenshots || img == nil || ev == nil {
		return
	}
	dir := a.screenshotDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}

	// 按天轮转：删除超过保留天数的旧截图
	if a.config.ScreenshotDays > 0 {
		cutoff := time.Now().Add(-time.Duration(a.config.ScreenshotDays) * 24 * time.Hour)
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				if info.ModTime().Before(cutoff) {
					_ = os.Remove(filepath.Join(dir, e.Name()))
				}
			}
		}
	}

	// 容量上限：默认目录超过 1GB 时，按修改时间从旧到新删除前 2/3
	// （只删修改时间早于当前的文件，最新的 1/3 始终保留）
	a.enforceScreenshotCap(dir)

	scaled := img
	if b := img.Bounds(); b.Dx() > 1280 {
		if s, _, _, _ := downscale(img, 1280); s != nil {
			scaled = s
		}
	}
	name := fmt.Sprintf("%s_%d.jpg", time.Now().Format("20060102_150405"), ev.ID)
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return
	}
	defer f.Close()
	if err := jpeg.Encode(f, scaled, &jpeg.Options{Quality: 82}); err != nil {
		log.Printf("[screenshot] 保存失败: %v", err)
	}
}

// enforceScreenshotCap 截图目录容量上限（1GB）：超限后按文件修改时间从旧到新
// 删除前 2/3 个文件，只处理修改时间早于当前的常规文件（正在写入/未来的文件不动）。
func (a *App) enforceScreenshotCap(dir string) {
	const capBytes = int64(1) << 30 // 1 GiB
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type fileInfo struct {
		path string
		size int64
		mod  time.Time
	}
	var files []fileInfo
	var total int64
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		// 只统计/删除"之前时间"的文件：修改时间晚于当前的跳过（防御时钟漂移）
		if info.ModTime().After(now) {
			continue
		}
		files = append(files, fileInfo{filepath.Join(dir, e.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= capBytes {
		return
	}
	// 按修改时间从旧到新排序
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	n := len(files) * 2 / 3 // 删前 2/3，保留最新 1/3
	var freed int64
	removed := 0
	for i := 0; i < n; i++ {
		if err := os.Remove(files[i].path); err == nil {
			freed += files[i].size
			removed++
		}
	}
	log.Printf("[screenshot] 目录容量 %.2fGB 超过 1GB 上限，已清理 %d 张旧截图（释放 %.2fGB），保留最新 %d 张",
		float64(total)/(1<<30), removed, float64(freed)/(1<<30), len(files)-removed)
}

// GetHeartbeat 获取状态条数据
func (a *App) GetHeartbeat() *types.Heartbeat {
	var lastAgo string
	if a.lastCapture.IsZero() {
		lastAgo = "—"
	} else {
		s := time.Since(a.lastCapture).Seconds()
		lastAgo = fmt.Sprintf("%.1fs 前", s)
	}
	hb := a.eng.GetHeartbeat(a.actualFps, a.droppedCount)
	hb.LastCaptureAgo = lastAgo
	return hb
}

// SetMode 切换运行模式
func (a *App) SetMode(m types.AppMode) {
	a.eng.SetMode(m)
}

// --- Wails 服务适配方法 ---

// Heartbeat 返回状态条数据（map 格式，供前端直接消费）
func (a *App) Heartbeat() map[string]any {
	hb := a.GetHeartbeat()
	if hb == nil {
		return map[string]any{}
	}
	screenAuthorized := a.IsScreenCaptureAuthorized()
	accessibilityAuthorized := a.IsAccessibilityAuthorized()
	mode := hb.Mode
	if !screenAuthorized {
		mode = types.ModeUnauthorized
	}
	return map[string]any{
		"mode":                     string(mode),
		"last_capture_ago":         hb.LastCaptureAgo,
		"actual_fps":               hb.ActualFps,
		"dropped_count":            hb.DroppedCount,
		"today_blocked":            hb.TodayBlocked,
		"today_reported":           hb.TodayReported,
		"today_false":              hb.TodayFalse,
		"imgsz":                    hb.Imgsz,
		"profile_date":             hb.ProfileDate,
		"screen_authorized":        screenAuthorized,
		"accessibility_authorized": accessibilityAuthorized,
	}
}

// SetModeStr 通过字符串切换模式（Wails 服务适配）。
// 同步更新内存配置并落盘：此前模式卡切换只改引擎内存，重启后回退旧模式。
func (a *App) SetModeStr(mode string) error {
	m := types.AppMode(mode)
	if !types.IsValidMode(m) {
		return fmt.Errorf("无效模式: %s", mode)
	}
	a.eng.SetMode(m)
	// 仅在变化时落盘，避免重复写盘
	a.mu.Lock()
	changed := a.config.Mode != m
	if changed {
		a.config.Mode = m
	}
	path := a.configPath
	var data []byte
	if changed && path != "" {
		data, _ = json.MarshalIndent(a.config, "", "  ")
	}
	a.mu.Unlock()
	if len(data) > 0 {
		_ = os.WriteFile(path, data, 0644)
		log.Printf("[Mode] 已切换为 %s 并持久化", mode)
	}
	return nil
}

// PersistConfig 强制把当前内存配置落盘（不论是否发生变化）。
// 存在的意义：SetModeStr 只在"模式发生变化"时写盘，而启动时把 extreme 降级为 auto
// 的分支会在 app.New 之前就把内存 mode 改成 auto，于是 SetModeStr 判定"无变化"
// 而不写盘 —— 配置文件会长期残留 extreme，误导后续排查。此方法用于补齐落盘。
func (a *App) PersistConfig() {
	a.mu.Lock()
	path := a.configPath
	data, _ := json.MarshalIndent(a.config, "", "  ")
	a.mu.Unlock()
	if path == "" || len(data) == 0 {
		return
	}
	_ = os.WriteFile(path, data, 0644)
	log.Printf("[Config] 已持久化配置到 %s", path)
}

// Pause 暂停监控
func (a *App) Pause(minutes int) error {
	a.eng.SetMode(types.ModePaused)
	return nil
}

// Resume 恢复监控（恢复到用户配置的真实模式，而非强制观察模式）
func (a *App) Resume() error {
	a.mu.Lock()
	mode := a.config.Mode
	a.mu.Unlock()
	if mode != types.ModeAuto && mode != types.ModeObserve && mode != types.ModeExtreme {
		mode = types.ModeObserve
	}
	a.eng.SetMode(mode)
	log.Printf("[Mode] 暂停恢复，回到 %s", mode)
	return nil
}

// UndoLastClick 撤销最近一次点击（PRD 3.9 "刚才那个不该点"）。
// 语义：点击后 3 秒窗口内可撤销；GUI/快捷键触发时调用。
// 当前实现为记录级撤销 —— 标记该次点击为"撤销/误点"，并触发一次复核回流
// （把那帧送进 review 队列，供人工标注）。真正在屏幕上"反悔"一次点击
// 在大多数弹窗场景无意义（窗口已消失），因此这里落"误点标记"而非再点一次。
func (a *App) UndoLastClick() error {
	a.lastClickMu.Lock()
	defer a.lastClickMu.Unlock()
	if !a.lastClickDone {
		return fmt.Errorf("没有可撤销的点击")
	}
	if time.Since(a.lastClickAt) > 3*time.Second {
		return fmt.Errorf("已超过 3 秒撤销窗口")
	}
	// 标记为误点，驱动数据飞轮（PRD 4.3.8）
	log.Printf("[Undo] 最近一次点击已标记为误点（%s 内）", time.Since(a.lastClickAt).Round(time.Millisecond))
	a.lastClickDone = false
	return nil
}

// RunSelfTest 运行自检（C8: 接 selftest 模块）
// 生成合成探针图 → 各档 imgsz 跑两级检测 → 落 profile
func (a *App) RunSelfTest() error {
	if a.detector == nil {
		return fmt.Errorf("推理器未初始化，无法自检")
	}
	w, h, _, err := a.capturer.ScreenSize()
	if err != nil {
		return fmt.Errorf("获取屏幕尺寸失败: %w", err)
	}
	probe := selftest.DefaultProbe(w, h)
	probeImg := selftest.DrawProbe(probe)

	result, err := selftest.Run(probeImg, probe, a.detector,
		[]int{1024, 1280, 1536}, a.config.ModelPath)
	if err != nil {
		return fmt.Errorf("自检失败: %w", err)
	}
	if result.Profile != nil {
		a.eng.SetProfile(result.Profile)
		if _, err := a.db.InsertProfile(result.Profile); err != nil {
			log.Printf("[selftest] profile 落盘失败: %v", err)
		}
	}
	log.Printf("[selftest] 完成: recommended=%d allPassed=%v reason=%s",
		result.Recommended, result.AllPassed, result.Reason)
	return nil
}

// GetEvents 获取事件流（C8: 接真实查询）
func (a *App) GetEvents(limit, offset int, filter string) ([]map[string]any, error) {
	return a.db.QueryEvents(limit, offset, filter)
}

// GetPolicyYAML 获取策略 YAML
func (a *App) GetPolicyYAML() (string, error) {
	return a.policyMgr.GetYAML()
}

// Shortcuts 返回配置中定义的全局快捷键（配置驱动，而非硬编码）。
// 配置为空时回落到平台默认值，保证用户不配也能用。
func (a *App) Shortcuts() (pause, freeze, undo string) {
	a.mu.Lock()
	cfg := a.config
	a.mu.Unlock()

	def := platform.DefaultGlobalShortcuts()
	pause, freeze, undo = def.Pause, def.Freeze, def.Undo
	if cfg == nil {
		return pause, freeze, undo
	}
	if s := strings.TrimSpace(cfg.GlobalShortcutPause); s != "" {
		pause = s
	}
	if s := strings.TrimSpace(cfg.GlobalShortcutFreeze); s != "" {
		freeze = s
	}
	if s := strings.TrimSpace(cfg.GlobalShortcutUndo); s != "" {
		undo = s
	}
	return pause, freeze, undo
}

// SavePolicy 保存策略
func (a *App) SavePolicy(yaml, note string) error {
	if err := a.policyMgr.SaveYAML(yaml, note); err != nil {
		return err
	}
	// 立即下发给引擎，避免等 10 分钟热更新轮询
	if snap := a.policyMgr.Current(); snap != nil {
		a.eng.SetPolicy(snap)
	}
	return nil
}

// SetConfigPath 记录配置文件路径，供 GetConfig/SaveConfig 持久化
func (a *App) SetConfigPath(p string) { a.configPath = p }

// GetConfig 返回当前配置（供前端设置页初始化）
func (a *App) GetConfig() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, _ := json.Marshal(a.config)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// SaveConfig 更新并持久化配置；若包含运行模式则即时切换。
func (a *App) SaveConfig(cfg map[string]any) error {
	a.mu.Lock()
	b, _ := json.Marshal(cfg)
	var updated types.AppConfig
	_ = json.Unmarshal(b, &updated)
	a.config = &updated
	path := a.configPath
	a.mu.Unlock()

	if path != "" {
		if data, err := json.MarshalIndent(updated, "", "  "); err == nil {
			_ = os.WriteFile(path, data, 0644)
		}
	}
	// 即时应用运行模式（设置页切 observe/auto 立即生效）
	if updated.Mode != "" {
		_ = a.SetModeStr(string(updated.Mode))
	}
	return nil
}

// GetCategoryPolicy 返回当前逐类策略（conf + mode），供前端调试台初始化
func (a *App) GetCategoryPolicy() map[string]policy.CategoryPolicy {
	return a.policyMgr.GetCategoryPolicy()
}

// SaveCategoryPolicy 保存逐类策略并热更新到引擎
func (a *App) SaveCategoryPolicy(updates map[string]policy.CategoryPolicy) error {
	snap, err := a.policyMgr.SaveCategoryPolicy(updates)
	if err != nil {
		return err
	}
	a.eng.SetPolicy(snap)
	return nil
}

// 注：数据库与事件流路径已统一到 internal/paths（~/Library/Application Support/ScreenGuard）。
// 之前这里的 DefaultDbPath/DefaultJsonlDir 基于 CWD 解析，会把数据写进任意启动目录，已删除。

// NumCores 获取物理核数
func NumCores() int {
	return runtime.NumCPU()
}

// DefaultInferThreads 默认推理线程数 = 物理核 50~70%
func DefaultInferThreads() int {
	n := runtime.NumCPU()
	threads := n * 6 / 10
	if threads < 1 {
		threads = 1
	}
	return threads
}

// Frames 返回帧推送器（供 main 注入 emitter 与前端 API 调用）
func (a *App) Frames() *FramePusher { return a.frames }

// SetFrameEmitter 注入 Wails 事件发送函数
func (a *App) SetFrameEmitter(f func(name string, payload any)) {
	if a.frames != nil {
		a.frames.SetEmitter(f)
	}
}

// GetLiveFrame 前端初次加载时主动拉最近一帧
func (a *App) GetLiveFrame() *FramePayload {
	if a.frames == nil {
		return nil
	}
	return a.frames.Latest()
}

// SetLiveEnabled 开关画布推送（关闭可省编码开销）
func (a *App) SetLiveEnabled(v bool) {
	if a.frames != nil {
		a.frames.SetEnabled(v)
	}
}

// FreezeFrame 冻结/解冻画布
func (a *App) FreezeFrame(v bool) {
	if a.frames != nil {
		a.frames.SetFrozen(v)
	}
}

// FrameStats 推送统计，用于诊断"画面为什么不动"
func (a *App) FrameStats() map[string]any {
	if a.frames == nil {
		return map[string]any{"enabled": false}
	}
	pushed, skipped, enabled, frozen := a.frames.Stats()
	return map[string]any{
		"pushed": pushed, "skipped": skipped,
		"enabled": enabled, "frozen": frozen,
	}
}

// SetIdleFunc 注入空闲查询函数（透传给引擎，红线 B5）
func (a *App) SetIdleFunc(f func() float64) {
	a.eng.SetIdleFunc(f)
}

// SetForbiddenZoneFunc 注入禁区判定函数（PRD 4.3.1 第 1 步）。
// 返回 (是否处于禁区, 原因串)。传 nil 表示平台未实现，循环跳过（保持旧行为）。
func (a *App) SetForbiddenZoneFunc(f func() (bool, string)) {
	a.mu.Lock()
	a.forbiddenFunc = f
	a.mu.Unlock()
	// 立即刷新一次，避免首帧判定滞后
	a.refreshForbiddenZone()
}

// ForbiddenZoneStatus 供心跳 / UI 展示当前禁区状态
func (a *App) ForbiddenZoneStatus() (bool, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.forbiddenIn, a.forbiddenReason
}

// refreshForbiddenZone 拉取一次禁区状态并下发给引擎
func (a *App) refreshForbiddenZone() {
	a.mu.Lock()
	f := a.forbiddenFunc
	a.mu.Unlock()
	if f == nil {
		return
	}
	in, reason := f()
	a.mu.Lock()
	a.forbiddenIn = in
	a.forbiddenReason = reason
	a.mu.Unlock()
	a.eng.SetForbiddenZone(in)
}

// forbiddenPollLoop 禁区轮询（1 秒）。
// 不放在抓屏循环里是因为抓屏失败 / 未授权时仍需持续判定，
// 且窗口状态变化（如刚切进远程桌面）需要比 2.5fps 更快的响应。
func (a *App) forbiddenPollLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			// 单轮 panic 隔离：该轮判定（含 Windows 前台窗口/远程桌面状态查询）
			// 在 RDP 切换、显示器状态变化等场景下可能 panic；若不加 recover，
			// panic 会逃出本 goroutine 直接把整个进程打死（无任何 crash.log /
			// Windows 错误事件，表现为"常驻时静默消失"）。这里每次迭代兜底，
			// 既保证进程存活，又能在 crash.log 留下堆栈供事后定位根因。
			func() {
				defer RecoverPanic("forbiddenPollLoop")
				a.refreshForbiddenZone()
			}()
		}
	}
}
