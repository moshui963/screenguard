package engine

import (
	"fmt"
	"math"
	"sync"
	"time"

	"screenguard/internal/types"
)

// ============================================================
// 决策引擎 — 实现 PRD 4.3 节全部核心业务流程规则
// ============================================================

// Engine 决策引擎，持有所有门控状态
type Engine struct {
	mu sync.Mutex

	// 运行状态
	mode    types.AppMode
	config  *types.AppConfig
	policy  *PolicySnapshot
	profile *types.DeviceProfile

	// 4 帧一致性门控（PRD 4.3.1 第7步）
	frameBuffer    []frameEntry // 最近 N 帧的候选中心
	frameNRequired int
	frameStdMax    float64
	frameTTL       time.Duration

	// 点击预算（PRD 4.3.2）
	lastClickPerContainer map[string]time.Time // 容器特征 → 上次点击时间
	clicksThisHour        int
	hourStart             time.Time
	failedPerClass        map[string]int // (类别) → 连续失败次数
	classCooldownUntil    map[string]time.Time

	// 弹窗状态机（PRD 4.4b）
	popupState types.PopupState

	// 用户空闲检测（红线 P1-3: 由平台层注入真实查询函数，
	// macOS 为 CGEventSourceSecondsSinceLastEventType；nil 时保守视为用户活跃）
	idleSeconds func() float64

	// 禁区状态
	inForbiddenZone bool

	// 帧计数
	frameIndex int64

	// 统计
	todayBlocked  int
	todayReported int
	todayFalse    int
}

// frameEntry 单帧候选记录
type frameEntry struct {
	ts      time.Time
	centers []types.Center // 当帧所有候选出口的中心
}

// PolicySnapshot 策略快照 — 版本号原子切换，进行中的决策用旧版本跑完
type PolicySnapshot struct {
	Version        string
	ConfThresholds map[string]float64          // 逐类置信度阈值
	ClassModes     map[string]types.PolicyMode // 逐类模式: click/report/disabled
	ForbiddenZones map[string]bool             // 禁区开关
	Blacklist      []BlacklistEntry
}

// BlacklistEntry 黑名单条目
type BlacklistEntry struct {
	Type    string
	Value   string
	Expires time.Time
}

// NewEngine 创建决策引擎
func NewEngine(cfg *types.AppConfig) *Engine {
	// 一致性门控下限保护：std < 2px 的门控低于 YOLO 检测框固有的 1~3px 抖动，
	// 会导致"确认中"永远不通过 → 自动模式下也永远不点击（v0.23 用户实测回归）。
	// 静止弹窗的中心抖动经 2px 量化后 std≈0~1；被拖动的弹窗位移远超门限，仍会被拦。
	effectiveStd := cfg.FrameConsistencyStd
	if effectiveStd < 2 {
		effectiveStd = 2
	}
	return &Engine{
		mode:                  cfg.Mode,
		config:                cfg,
		frameNRequired:        cfg.FrameConsistencyN,
		frameStdMax:           effectiveStd,
		frameTTL:              time.Duration(cfg.FrameConsistencyTTL) * time.Millisecond,
		lastClickPerContainer: make(map[string]time.Time),
		failedPerClass:        make(map[string]int),
		classCooldownUntil:    make(map[string]time.Time),
		popupState:            types.PopupIDLE,
		hourStart:             time.Now(),
	}
}

// SetIdleFunc 注入真实空闲查询函数（红线 P1-3）
func (e *Engine) SetIdleFunc(f func() float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.idleSeconds = f
}

// SetMode 切换运行模式
func (e *Engine) SetMode(m types.AppMode) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mode = m
}

// SetPolicy 原子切换策略版本
func (e *Engine) SetPolicy(p *PolicySnapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policy = p
}

// SetProfile 设置设备 profile
func (e *Engine) SetProfile(p *types.DeviceProfile) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.profile = p
}

// SetForbiddenZone 设置禁区状态
func (e *Engine) SetForbiddenZone(in bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inForbiddenZone = in
}

// ============================================================
// Decide 执行完整决策优先级链（PRD 4.3.1）
// 顺序不可变，任一环节拒绝即终止并记原因
// ============================================================
func (e *Engine) Decide(boxes []types.DetectionBox, screenW, screenH int, captureMs, modelMs int) *types.DetectionEvent {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.frameIndex++
	totalMs := captureMs + modelMs

	event := &types.DetectionEvent{
		Ts:         time.Now(),
		SessionID:  e.config.ModelPath, // 简化
		FrameIndex: e.frameIndex,
		ScreenW:    screenW,
		ScreenH:    screenH,
		CaptureMs:  captureMs,
		ModelMs:    modelMs,
		TotalMs:    totalMs,
		Boxes:      boxes,
	}
	if e.profile != nil {
		event.ProfileID = e.profile.ID
	}
	if e.policy != nil {
		event.PolicyVersion = e.policy.Version
	}

	// ---- 1. 禁区检查 ----
	if e.inForbiddenZone || e.mode == types.ModePaused {
		event.Action = types.ActionBlockedForbiddenZone
		event.Result = types.ResultNone
		e.recordBlocked()
		return event
	}

	// ---- 2. 用户空闲检测（红线 P1-3: 直接查询系统，优先级高于任何模型判断）----
	// 极限模式例外：不再理会用户是否在操作，检测到关闭目标立即处理。
	idleSec := 0.0 // nil 时保守视为用户活跃（刚输入），不点击
	if e.idleSeconds != nil {
		idleSec = e.idleSeconds()
	}
	if idleSec < 1.5 && e.mode != types.ModeExtreme {
		event.Action = types.ActionBlockedUserActive
		event.Result = types.ResultNone
		e.popupState = types.PopupSUSPECT // 退回 SUSPECT，计数不清零
		e.recordBlocked()
		return event
	}

	// ---- 3. profile 门禁 ----
	// 分离容器和出口
	containers := filterBoxes(boxes, func(b types.DetectionBox) bool {
		return b.Role == types.RoleContainer
	})
	exits := filterBoxes(boxes, func(b types.DetectionBox) bool {
		return b.Role == types.RoleExit
	})

	// 检查每个出口的 profile mode
	for i := range exits {
		mode := e.getClassMode(exits[i].ClSName)
		if mode == types.PolicyDisabled {
			exits[i].GatePassed = false
			exits[i].RejectReason = "disabled_class"
		}
	}

	// 过滤掉被禁用的
	activeExits := filterBoxes(exits, func(b types.DetectionBox) bool {
		return b.GatePassed || b.RejectReason == ""
	})

	// ---- 4. 容器约束 ----
	for i := range activeExits {
		if e.isInContainer(activeExits[i].Center, containers) {
			activeExits[i].GatePassed = true
		} else {
			activeExits[i].GatePassed = false
			activeExits[i].RejectReason = "outside_container"
		}
	}
	inContainerExits := filterBoxes(activeExits, func(b types.DetectionBox) bool {
		return b.GatePassed
	})

	if len(inContainerExits) == 0 {
		// 所有出口都在容器外
		if len(activeExits) > 0 {
			event.Action = types.ActionBlockedOutsideContainer
		} else if len(containers) > 0 {
			// 弹窗本体在，但这一帧未检出任何关闭按钮（✕ 漏检/被遮挡/窗口拖动中）。
			// 给出明确拒因，前端据此显示"未检出关闭按钮"，而不是误标成"目标确认中"。
			event.Action = types.ActionReportOnly
			event.PreemptedCause = "no_exit"
		} else {
			// 屏幕上什么都没检出：不产生事件，避免事件流被"发现「—」未点击（）"空噪声刷屏
			return nil
		}
		event.Result = types.ResultNone
		e.recordBlocked()
		return event
	}

	// ---- 5. 几何合法性 ----
	inContainerExits = e.geometricDedup(inContainerExits)

	// ---- 6. 逐类阈值 ----
	var passed []types.DetectionBox
	for _, b := range inContainerExits {
		thr := e.getConfThreshold(b.ClSName)
		if b.Conf >= thr {
			b.GatePassed = true
			passed = append(passed, b)
		} else {
			b.GatePassed = false
			b.RejectReason = "low_conf"
		}
	}

	if len(passed) == 0 {
		event.Action = types.ActionLowConf
		event.Result = types.ResultNone
		e.recordBlocked()
		return event
	}

	// ---- 7. 4帧一致性 ----
	e.frameBuffer = append(e.frameBuffer, frameEntry{
		ts:      time.Now(),
		centers: extractCenters(passed),
	})
	// 清理 TTL 外的帧
	e.pruneFrameBuffer()

	if !e.checkConsistency() {
		if e.frameTTLExceeded() {
			event.Action = types.ActionGaveUpTimeout
			e.resetFrameBuffer()
		} else {
			event.Action = types.ActionReportOnly
			e.popupState = types.PopupSUSPECT
		}
		event.Result = types.ResultNone
		e.recordBlocked()
		return event
	}

	// ---- 8. 点击预算 ----
	// 选最优出口（最高置信度）
	best := selectBest(passed)
	containerKey := containerKey(containers, best)

	// 同容器 2s 内已点过
	if lastT, ok := e.lastClickPerContainer[containerKey]; ok {
		if time.Since(lastT) < time.Duration(e.config.SuppressRapidMs)*time.Millisecond {
			event.Action = types.ActionSuppressedRapid
			event.Result = types.ResultNone
			e.recordBlocked()
			return event
		}
	}

	// 类别降频检查
	if until, ok := e.classCooldownUntil[best.ClSName]; ok && time.Now().Before(until) {
		event.Action = types.ActionSuppressedRapid
		event.Result = types.ResultNone
		e.recordBlocked()
		return event
	}

	// 全局每小时预算
	if time.Since(e.hourStart) > time.Hour {
		e.clicksThisHour = 0
		e.hourStart = time.Now()
	}
	if e.clicksThisHour >= e.config.ClickBudgetPerHour {
		event.Action = types.ActionBudgetExceeded
		event.Result = types.ResultNone
		e.recordBlocked()
		return event
	}

	// ---- 9. 执行点击 ----
	// 全局自动模式 → 点击；观察模式下，若命中类别的逐类策略为 click（用户在调试台
	// 单独把某类设为"自动点"），则对该类别也执行真实点击，而不是只上报。
	// 这样调试台的"自动点 / 只上报 / 禁用"三种逐类模式在观察模式下都有真实含义：
	//   click     → 真的点
	//   report    → 只上报（默认安全行为）
	//   disabled  → 关闭该类别（第 3 步已 gate off）
	bestMode := e.getClassMode(best.ClSName)
	if e.mode == types.ModeAuto || e.mode == types.ModeExtreme || bestMode == types.PolicyClick {
		event.Action = types.ActionClick
		e.popupState = types.PopupARMED
		e.clicksThisHour++
		e.lastClickPerContainer[containerKey] = time.Now()
		// 清空帧缓冲
		e.resetFrameBuffer()
	} else {
		// 观察模式只上报
		event.Action = types.ActionReportOnly
		e.popupState = types.PopupSUSPECT
		e.recordReported()
	}

	return event
}

// RecordClickResult 记录点击后的验证结果
func (e *Engine) RecordClickResult(clsName string, result types.Result) {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch result {
	case types.ResultVanished:
		e.popupState = types.PopupCLOSED
		e.failedPerClass[clsName] = 0
	case types.ResultFailed:
		e.popupState = types.PopupFAILED
		e.failedPerClass[clsName]++
		if e.failedPerClass[clsName] >= 3 {
			// 降频 10 分钟
			e.classCooldownUntil[clsName] = time.Now().Add(10 * time.Minute)
			e.popupState = types.PopupCOOLDOWN
		}
	case types.ResultRetry:
		e.popupState = types.PopupRETRY
	}
}

// ============================================================
// 内部辅助方法
// ============================================================

func (e *Engine) getClassMode(clsName string) types.PolicyMode {
	if e.policy == nil {
		return types.PolicyReport // 默认安全
	}
	if m, ok := e.policy.ClassModes[clsName]; ok {
		return m
	}
	return types.PolicyReport
}

func (e *Engine) getConfThreshold(clsName string) float64 {
	if e.policy == nil {
		return 0.5
	}
	if t, ok := e.policy.ConfThresholds[clsName]; ok {
		return t
	}
	return 0.4
}

// isInContainer 检查出口中心是否落在某个容器框内
// pad = 出口短边的 50%（PRD 4.3.1 第4步）
func (e *Engine) isInContainer(center types.Center, containers []types.DetectionBox) bool {
	for _, c := range containers {
		pad := minDim(c.Box) * 0.5
		if center[0] >= c.Box[0]-pad && center[0] <= c.Box[2]+pad &&
			center[1] >= c.Box[1]-pad && center[1] <= c.Box[3]+pad {
			return true
		}
	}
	return false
}

// geometricDedup 几何去重 — 出口面积 > 容器 25% 判 dup
func (e *Engine) geometricDedup(exits []types.DetectionBox) []types.DetectionBox {
	var kept []types.DetectionBox
	for i, b := range exits {
		isDup := false
		for j, other := range exits {
			if i == j {
				continue
			}
			iou := boxIoU(b.Box, other.Box)
			if iou > 0.30 {
				// 保留置信度更高的
				if other.Conf > b.Conf {
					isDup = true
					break
				}
			}
		}
		if isDup {
			exits[i].GatePassed = false
			exits[i].RejectReason = "blocked_dup"
		} else {
			kept = append(kept, exits[i])
		}
	}
	return kept
}

// checkConsistency 检查 4 帧一致性 — std < 1px
func (e *Engine) checkConsistency() bool {
	if len(e.frameBuffer) < e.frameNRequired {
		return false
	}
	// 取最近 N 帧
	start := len(e.frameBuffer) - e.frameNRequired
	recent := e.frameBuffer[start:]
	if len(recent) < e.frameNRequired {
		return false
	}
	// 对每帧的第一个中心，算 std
	var xs, ys []float64
	for _, f := range recent {
		if len(f.centers) == 0 {
			return false
		}
		xs = append(xs, f.centers[0][0])
		ys = append(ys, f.centers[0][1])
	}
	stdX := stdDev(xs)
	stdY := stdDev(ys)
	return math.Max(stdX, stdY) < e.frameStdMax
}

func (e *Engine) pruneFrameBuffer() {
	cutoff := time.Now().Add(-e.frameTTL)
	var kept []frameEntry
	for _, f := range e.frameBuffer {
		if f.ts.After(cutoff) {
			kept = append(kept, f)
		}
	}
	e.frameBuffer = kept
}

func (e *Engine) frameTTLExceeded() bool {
	if len(e.frameBuffer) == 0 {
		return false
	}
	return time.Since(e.frameBuffer[0].ts) > e.frameTTL
}

func (e *Engine) resetFrameBuffer() {
	e.frameBuffer = nil
}

func (e *Engine) recordBlocked() {
	e.todayBlocked++
}
func (e *Engine) recordReported() {
	e.todayReported++
}

// GetHeartbeat 获取状态条数据
func (e *Engine) GetHeartbeat(actualFps float64, dropped int) *types.Heartbeat {
	e.mu.Lock()
	defer e.mu.Unlock()
	var imgsz int
	var profileDate string
	if e.profile != nil {
		imgsz = e.profile.Imgsz
		profileDate = e.profile.CreatedAt.Format("2006-01-02")
	}
	return &types.Heartbeat{
		Mode:          e.mode,
		ActualFps:     actualFps,
		DroppedCount:  dropped,
		TodayBlocked:  e.todayBlocked,
		TodayReported: e.todayReported,
		TodayFalse:    e.todayFalse,
		Imgsz:         imgsz,
		ProfileDate:   profileDate,
	}
}

// ============================================================
// 纯函数工具
// ============================================================

func filterBoxes(boxes []types.DetectionBox, pred func(types.DetectionBox) bool) []types.DetectionBox {
	var out []types.DetectionBox
	for _, b := range boxes {
		if pred(b) {
			out = append(out, b)
		}
	}
	return out
}

func extractCenters(boxes []types.DetectionBox) []types.Center {
	// 2px 网格量化：吸收检测框的亚像素级抖动，避免 std 门控被噪声卡死。
	// 真实移动中的弹窗帧间位移远大于 2px，量化不影响对移动目标的拦截。
	var cs []types.Center
	for _, b := range boxes {
		cs = append(cs, types.Center{
			float64(int(math.Round(b.Center[0]/2))) * 2,
			float64(int(math.Round(b.Center[1]/2))) * 2,
		})
	}
	return cs
}

func selectBest(boxes []types.DetectionBox) types.DetectionBox {
	best := boxes[0]
	for _, b := range boxes[1:] {
		if b.Conf > best.Conf {
			best = b
		}
	}
	return best
}

func containerKey(containers []types.DetectionBox, exit types.DetectionBox) string {
	// 找到出口所在的容器
	for _, c := range containers {
		if exit.Center[0] >= c.Box[0] && exit.Center[0] <= c.Box[2] &&
			exit.Center[1] >= c.Box[1] && exit.Center[1] <= c.Box[3] {
			return fmt.Sprintf("%d_%d_%d_%d",
				int(c.Box[0]/10), int(c.Box[1]/10), int(c.Box[2]/10), int(c.Box[3]/10))
		}
	}
	return "unknown"
}

func boxIoU(a, b types.BoxXYXY) float64 {
	iw := math.Min(a[2], b[2]) - math.Max(a[0], b[0])
	ih := math.Min(a[3], b[3]) - math.Max(a[1], b[1])
	if iw <= 0 || ih <= 0 {
		return 0
	}
	inter := iw * ih
	areaA := (a[2] - a[0]) * (a[3] - a[1])
	areaB := (b[2] - b[0]) * (b[3] - b[1])
	union := areaA + areaB - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

func minDim(b types.BoxXYXY) float64 {
	w := b[2] - b[0]
	h := b[3] - b[1]
	if w < h {
		return w
	}
	return h
}

func stdDev(v []float64) float64 {
	if len(v) < 2 {
		return 0
	}
	mean := 0.0
	for _, x := range v {
		mean += x
	}
	mean /= float64(len(v))
	sumSq := 0.0
	for _, x := range v {
		d := x - mean
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(v)-1))
}
