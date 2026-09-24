package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"screenguard/frontend"
	"screenguard/internal/app"
	"screenguard/internal/capture"
	"screenguard/internal/click"
	"screenguard/internal/infer"
	"screenguard/internal/paths"
	"screenguard/internal/platform"
	"screenguard/internal/policy"
	"screenguard/internal/types"
	"screenguard/internal/version"
)

// ============================================================
// 屏净 ScreenGuard — 主进程入口 (Wails v3)
// ============================================================

// ScreenGuardService 暴露给前端的 API 服务
type ScreenGuardService struct {
	app      *app.App
	wailsApp *application.App
}

// Version 返回编译期注入的版本号（每次编译本体自动 +0.01）。
func (s *ScreenGuardService) Version() string {
	return version.Version
}

func (s *ScreenGuardService) GetHeartbeat() map[string]any {
	defer guardService("GetHeartbeat")
	h := s.app.Heartbeat()
	h["version"] = version.Version
	return h
}

// guardService 捕获前端触发的绑定方法 panic，把堆栈打到控制台日志。
// 绑定方法里的 panic 以前会直接杀死整个进程（"点一下模式卡就崩"的排查抓手）；
// 恢复后前端收到的只是本次调用失败，应用继续运行，且终端里能看到完整堆栈。
func guardService(name string) {
	if r := recover(); r != nil {
		log.Printf("[PANIC] 绑定方法 %s 发生 panic（已恢复，进程继续运行）: %v\n%s",
			name, r, debug.Stack())
	}
}

// Shortcuts 返回当前生效的全局快捷键组合（配置驱动，配置为空时用平台默认值）。
func (s *ScreenGuardService) Shortcuts() (pause, freeze, undo string) {
	return s.app.Shortcuts()
}

func (s *ScreenGuardService) SetMode(mode string) error {
	defer guardService("SetMode")
	log.Printf("[Mode] 前端请求切换模式 → %s", mode)
	err := s.app.SetModeStr(mode)
	if err != nil {
		log.Printf("[Mode] 切换失败: %v", err)
	} else {
		log.Printf("[Mode] 切换成功，当前模式 = %s", mode)
	}
	return err
}

func (s *ScreenGuardService) Pause(minutes int) error {
	defer guardService("Pause")
	return s.app.Pause(minutes)
}

func (s *ScreenGuardService) Resume() error {
	defer guardService("Resume")
	return s.app.Resume()
}

// UndoLastClick 撤销最近一次点击（"刚才那个不该点"，3 秒窗口）
func (s *ScreenGuardService) UndoLastClick() error {
	return s.app.UndoLastClick()
}

func (s *ScreenGuardService) GetPermissions() map[string]any {
	return s.app.PermissionStatus()
}

func (s *ScreenGuardService) RequestScreenCapturePermission() bool {
	return s.app.RequestScreenCapturePermission()
}

func (s *ScreenGuardService) RequestAccessibilityPermission() bool {
	return s.app.RequestAccessibilityPermission()
}

func (s *ScreenGuardService) RunSelfTest() error {
	return s.app.RunSelfTest()
}

// GetLiveFrame 拉最近一帧（前端初次加载用，避免等下一个事件）
func (s *ScreenGuardService) GetLiveFrame() *app.FramePayload {
	return s.app.GetLiveFrame()
}

// SetLiveEnabled 开关画布推送
func (s *ScreenGuardService) SetLiveEnabled(v bool) {
	s.app.SetLiveEnabled(v)
}

// FreezeFrame 冻结/解冻画布
func (s *ScreenGuardService) FreezeFrame(v bool) {
	s.app.FreezeFrame(v)
}

// GetFrameStats 推送统计（诊断"画面不动"）
func (s *ScreenGuardService) GetFrameStats() map[string]any {
	return s.app.FrameStats()
}

// QuitApp 退出应用
func (s *ScreenGuardService) QuitApp() {
	go func() {
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}()
}

// SelectScreenshotDir 调用系统目录选择对话框，返回用户选择的截图保存目录
func (s *ScreenGuardService) SelectScreenshotDir() string {
	if s.wailsApp == nil {
		return ""
	}
	dialog := s.wailsApp.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		SetTitle("选择截图保存位置")
	res, err := dialog.PromptForSingleSelection()
	if err != nil || res == "" {
		return ""
	}
	return res
}

// RestartApp 授权后以 bundle 身份重启（保证 TCC 身份一致）
func (s *ScreenGuardService) RestartApp() {
	s.app.RestartApp()
}

func (s *ScreenGuardService) GetEvents(limit, offset int, filter string) ([]map[string]any, error) {
	return s.app.GetEvents(limit, offset, filter)
}

func (s *ScreenGuardService) GetPolicy() (string, error) {
	return s.app.GetPolicyYAML()
}

func (s *ScreenGuardService) SavePolicy(yamlStr, note string) error {
	return s.app.SavePolicy(yamlStr, note)
}

// GetCategoryPolicy 返回当前逐类策略（conf + mode），供前端调试台初始化
func (s *ScreenGuardService) GetCategoryPolicy() (map[string]policy.CategoryPolicy, error) {
	return s.app.GetCategoryPolicy(), nil
}

// SaveCategoryPolicy 保存逐类策略并热更新到引擎
func (s *ScreenGuardService) SaveCategoryPolicy(updates map[string]policy.CategoryPolicy) error {
	return s.app.SaveCategoryPolicy(updates)
}

// GetConfig 返回当前应用配置（设置页初始化；修复 "保存设置没反应" 的绑定缺失）
func (s *ScreenGuardService) GetConfig() (map[string]any, error) {
	defer guardService("GetConfig")
	return s.app.GetConfig(), nil
}

// SaveConfig 更新并持久化配置（含运行模式即时切换）
func (s *ScreenGuardService) SaveConfig(cfg map[string]any) error {
	defer guardService("SaveConfig")
	return s.app.SaveConfig(cfg)
}

func main() {
	// 双击 exe 启动时分配系统控制台窗口（"终端页面"），与前端 GUI 并存；
	// 终端内运行（开发期）自动跳过，不会多弹黑框。
	ensureConsole()

	// 崩溃兜底：主协程 panic 时把完整堆栈写入 crash.log。
	// GUI 子系统下 stderr 不可见，没有这层记录就永远定位不到"启动即崩退"。
	defer app.RecoverPanic("main")

	devMode := flag.Bool("dev", false, "开发模式（前端走 Vite dev server）")
	configPath := flag.String("config", "configs/config.json", "配置文件路径")
	flag.Parse()

	log.Printf("[ScreenGuard] 版本 v%s | 平台=%s/%s", version.Version, runtime.GOOS, runtime.GOARCH)

	// 平台初始化：必须在创建任何窗口之前完成。
	// Windows 侧这里声明 DPI 感知，保证全链路物理像素（PRD 4.3.5 红线）。
	initPlatform()

	// ============ 运行时路径（各平台规范，见 internal/paths 注释）============
	// 配置与数据一律放 ~/Library/Application Support/ScreenGuard/，
	// 不再依赖 CWD 或 .app 的父目录 —— 否则 app 移到哪就把数据写到哪。
	userDir, err := paths.AppDir()
	if err != nil {
		log.Fatalf("无法创建用户数据目录: %v", err)
	}

	// 配置文件：-config 显式指定则尊重；否则用用户目录
	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = filepath.Join(userDir, "config.json")
	} else if !filepath.IsAbs(cfgPath) {
		// 相对路径：先在用户目录找，再按 CWD 解析（开发期显式传参用）
		if cand := filepath.Join(userDir, filepath.Base(cfgPath)); fileExists(cand) {
			cfgPath = cand
		} else if abs, e := filepath.Abs(cfgPath); e == nil && fileExists(abs) {
			cfgPath = abs
		} else {
			cfgPath = filepath.Join(userDir, "config.json")
		}
	}
	cfg := loadConfig(cfgPath)

	// 日志统一集中到"软件目录/log"（可用 config.json 的 log_dir 覆盖）。
	// 必须在 loadConfig 之后：日志目录本身由配置决定。
	logDir := resolveLogDir(cfg, userDir)
	logFile := setupLogger(logDir)
	if logFile != nil {
		defer logFile.Close()
	}
	pruneOldLogs(logDir, cfg.LogRetentionDays)

	// 崩溃堆栈也进软件目录/log/crash.log（用户要求"所有日志"都在软件目录，
	// 不再散落到 %APPDATA%）。必须尽早设置：main 协程的 RecoverPanic 兜底在任何
	// 初始化 panic 时都会用到这个路径。
	app.SetCrashLogPath(filepath.Join(logDir, "crash.log"))

	log.Printf("[Paths] 用户数据目录: %s", userDir)

	// 身份自检（平台相关，Windows 无 TCC 概念，为空实现）
	warnBundleIdentity()

	// 策略文件：统一到用户目录，首次运行从 bundle/源码树复制默认策略
	if cfg.PolicyPath == "" || !filepath.IsAbs(cfg.PolicyPath) {
		cfg.PolicyPath = filepath.Join(userDir, "policy.yaml")
	}
	ensurePolicy(cfg.PolicyPath)

	// 模型：bundle Resources/models > 用户目录 models > 源码树
	cfg.ModelPath = paths.ModelPath(cfg.ModelPath)

	dbPath := filepath.Join(userDir, "guard.db")
	// 事件流 JSONL 属于日志范畴，一并放进软件目录/log/events
	jsonlDir := filepath.Join(logDir, "events")
	os.MkdirAll(filepath.Dir(dbPath), 0o755)
	os.MkdirAll(jsonlDir, 0o755)
	logPathsSummary(userDir, logDir, cfgPath, dbPath)

	// 极限模式不跨会话保留：若上次退出时停留在 extreme，本次启动降级为自动模式。
	// 原因：extreme 会无视用户是否在操作、劫持鼠标去点屏幕上所有关闭按钮；
	// 一旦随开机自启进入该状态，用户会陷入"鼠标被抢、窗口被关"的失控状态，
	// 且配置残留会让"启动即失控"反复复现。需要时请在监控台手动开启。
	wasExtreme := cfg.Mode == types.ModeExtreme
	if wasExtreme {
		log.Printf("[Mode] 上次停留在极限模式 → 本次启动降级为自动模式（极限模式不随开机自启，需手动开启）")
		cfg.Mode = types.ModeAuto
	}

	a, err := app.New(cfg, dbPath, jsonlDir)
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}
	// 注：把降级结果写回配置文件的动作在下方 SetConfigPath 之后执行
	// （在那之前 App 还不知道配置文件路径，写盘会被跳过）

	capturer := newCapturer()
	if capturer != nil {
		a.SetCapturer(capturer)
		if !capturer.IsAuthorized() {
			log.Println("[WARN] 屏幕录制权限未授予，进入未授权态")
			cfg.Mode = types.ModeUnauthorized
		}
	} else {
		log.Println("[WARN] 抓屏器不可用")
		cfg.Mode = types.ModeUnauthorized
	}

	detector, err := newDetector(cfg)
	if err != nil {
		log.Printf("[WARN] 推理器初始化失败: %v", err)
	} else {
		a.SetDetector(detector)
	}

	clicker := newClicker(capturer)
	if clicker != nil {
		a.SetClicker(clicker)
	}

	// 红线 B5 / P1-3: 注入真实用户空闲检测
	//   macOS   → CGEventSourceSecondsSinceLastEventType
	//   Windows → GetLastInputInfo
	a.SetIdleFunc(platformIdleSeconds)

	// PRD 4.3.1 第 1 步: 注入禁区判定（Windows 无 TCC，此项为唯一安全边界）
	if fz := platformForbiddenZone(); fz != nil {
		a.SetForbiddenZoneFunc(fz)
	}

	a.Start()

	// 记录配置文件路径，供设置页 GetConfig/SaveConfig 持久化
	a.SetConfigPath(cfgPath)

	// 极限模式降级结果写回配置文件，避免长期残留 extreme。
	// 必须在 SetConfigPath 之后：此前 configPath 为空，PersistConfig 会直接返回。
	if wasExtreme {
		a.PersistConfig()
	}

	log.Printf("[ScreenGuard] 已启动 | 版本=v%s | 模式=%s | 线程数=%d | 平台=%s/%s",
		version.Version, cfg.Mode, app.DefaultInferThreads(), runtime.GOOS, runtime.GOARCH)

	// 创建 Wails v3 应用
	svc := &ScreenGuardService{app: a}
	var mainWindow application.Window
	url := ""
	if *devMode {
		url = "http://localhost:5173"
	}

	// 前端静态资源：
	//   dev 模式  → 反代 Vite dev server（URL 指向 localhost:5173）
	//   生产模式  → 使用 go:embed 编进二进制的 frontend/dist，位置无关，
	//              .app 移到 /Applications / 拷给同事 / 只读卷都不会坏。
	var assetHandler http.Handler
	if *devMode {
		assetHandler = http.NotFoundHandler() // dev 模式下通过 URL 直接访问 Vite
	} else {
		distFS, err := fs.Sub(frontend.Dist, "dist")
		if err != nil {
			log.Fatalf("[Assets] 无法访问嵌入的前端资源: %v", err)
		}
		// 校验嵌入内容非空（只有 .keep 时说明忘了先跑 npm run build）
		if entries, e := fs.ReadDir(distFS, "."); e != nil || len(entries) <= 1 {
			log.Printf("[WARN] 嵌入的前端资源为空。请先 `npm run build` 产出 frontend/dist 再编译。")
		}
		assetHandler = application.BundledAssetFileServer(distFS)
		log.Printf("[Assets] 使用嵌入的前端资源（go:embed）")
	}

	wailsApp := application.New(application.Options{
		Name:        "屏净 ScreenGuard",
		Description: "自动识别并关闭桌面弹窗",
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.screenguard.app",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				log.Println("[SingleInstance] 检测到二次启动，唤起已有主窗口")
				if mainWindow != nil {
					if mainWindow.IsMinimised() {
						mainWindow.Restore()
					}
					mainWindow.Show()
					mainWindow.Focus()
					log.Println("[SingleInstance] 主窗口已唤起并置前")
				} else {
					log.Println("[WARN] 二次启动时主窗口句柄为空，无法唤起")
				}
			},
			ExitCode: 0,
		},
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: assetHandler,
		},
	})

	// 帧推送：把 Wails 事件总线接到 App（画布数据源）
	a.SetFrameEmitter(func(name string, payload any) {
		wailsApp.Event.Emit(name, payload)
	})

	// 创建主窗口：必须通过 wailsApp.Window 管理器创建，否则窗口不会挂到应用运行循环
	windowOptions := application.WebviewWindowOptions{
		Name:   "main",
		Title:  "屏净 ScreenGuard",
		Width:  1280,
		Height: 800,
	}
	if url != "" {
		windowOptions.URL = url
	}
	mainWindow = wailsApp.Window.NewWithOptions(windowOptions)
	log.Printf("[Window] 主窗口已创建 (name=%s)", windowOptions.Name)

	// 关闭窗口 = 隐藏到托盘，而不是销毁。关键修复：此前直接关闭窗口时
	// Wails v3 会销毁窗口，之后无论是托盘"打开主窗口"还是二次启动的
	// Show()，作用在已销毁窗口上都是 no-op —— 表现为"双击 exe 只出终端、
	// GUI 永远不出来"。Cancel 掉关闭事件并 Hide，窗口常驻可随时唤回。
	mainWindow.OnWindowEvent(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		mainWindow.Hide()
		log.Println("[Window] 主窗口已隐藏到托盘（点关闭 ≠ 退出；托盘右键 → 退出）")
	})

	wailsApp.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		// 启动即时把主窗口显示并置前：此前窗口虽已创建（非 Hidden），
		// 但 WebView2 首帧渲染前窗口可能停在后台/未激活，导致用户"看不到窗口、
		// 必须右键托盘才出来"。这里同步 Show+Center+Focus 确保在主线程立即置前。
		log.Println("[Window] ApplicationStarted 事件到达，执行 Show+Center+Focus")
		mainWindow.Center()
		mainWindow.Show()
		mainWindow.Focus()

		go func() {
			// 该 goroutine 调用 Windows API（SetWindowDisplayAffinity / 全局快捷键注册），
			// 任何异常都不应拖垮主进程：用 RecoverPanic 兜底，panic 仅记日志不致命。
			defer app.RecoverPanic("selfguard+hotkeys")
			// 自激防护（PRD 4.3.7）：窗口已存在，排除当前进程全部顶层窗口，
			// 使主窗口/调试台/探针不被自身抓屏拍到，避免正反馈。
			if err := a.ExcludeSelfWindows(); err != nil {
				log.Printf("[WARN] 自激防护窗口排除失败（旧系统？靠禁区兜底）: %v", err)
			} else {
				log.Println("[SelfGuard] 已排除自身窗口，防止抓屏自激")
			}

			// 全局快捷键必须在应用启动后注册：此时 Wails 的 GlobalShortcutManager
			// 已完成 flushPending，Register 会同步返回真实结果，被占用时才能回退。
			// 此处位于独立 goroutine，Register 内部会 InvokeSync 回主线程，不会死锁。
			registerGlobalShortcuts(wailsApp, svc)
		}()
	})

	ctx := context.Background()
	_ = ctx

	// ===== Windows 桌面端标准能力（PRD 3.9 / 3.10）=====
	// 优先复用 Wails v3 内置实现，不自造 Win32 轮子：
	//   - 单实例：application.Options.SingleInstance（已在上方装配 UniqueID）
	//   - 开机自启：wailsApp.Autostart（HKCU Run 注册表）
	//   - 全局快捷键：wailsApp.GlobalShortcut.Register（RegisterHotKey）
	//   - 系统托盘：wailsApp.SystemTray.New() + Menu
	setupDesktopStandardFeatures(wailsApp, svc, mainWindow)

	// 把 wailsApp 注入服务，供"选择目录"对话框等需要时调用
	svc.wailsApp = wailsApp

	if err := wailsApp.Run(); err != nil {
		log.Fatalf("应用退出: %v", err)
	}
}

func loadConfig(path string) *types.AppConfig {
	cfg := types.DefaultConfig()
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			log.Printf("[WARN] 配置解析失败，使用默认: %v", err)
			cfg = types.DefaultConfig()
		}
	} else {
		os.MkdirAll(filepath.Dir(path), 0755)
		data, _ := json.MarshalIndent(cfg, "", "  ")
		os.WriteFile(path, data, 0644)
		log.Printf("[Config] 已写入默认配置到 %s", path)
	}
	return cfg
}

func newCapturer() capture.Capturer {
	return newPlatformCapturer()
}

func newDetector(cfg *types.AppConfig) (infer.Detector, error) {
	modelPath := cfg.ModelPath
	if modelPath == "" {
		modelPath = "tangchuang/yolo26m.onnx"
	}
	threads := cfg.InferThreads
	if threads <= 0 {
		threads = app.DefaultInferThreads()
	}
	detector, err := infer.NewONNXDetector(modelPath, threads)
	if err != nil {
		return nil, fmt.Errorf("加载 ONNX 模型 %s: %w", modelPath, err)
	}
	log.Printf("[infer] ONNX 模型已加载: %s, threads=%d", modelPath, threads)
	return detector, nil
}

func newClicker(cap capture.Capturer) click.Clicker {
	return click.NewPlatformClicker(cap)
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// ensurePolicy 保证用户目录有一份可读写的 policy.yaml：
// 首次运行从 bundle Resources 或源码树 configs/ 复制，之后用户可就地编辑。
func ensurePolicy(dst string) {
	if fileExists(dst) {
		return
	}
	candidates := []string{}
	if res := paths.BundleResourcesDir(); res != "" {
		candidates = append(candidates, filepath.Join(res, "configs", "policy.yaml"))
	}
	if abs, e := filepath.Abs("configs/policy.yaml"); e == nil {
		candidates = append(candidates, abs)
	}
	for _, src := range candidates {
		if data, err := os.ReadFile(src); err == nil {
			os.MkdirAll(filepath.Dir(dst), 0o755)
			if os.WriteFile(dst, data, 0o644) == nil {
				log.Printf("[Paths] 已初始化默认策略: %s → %s", src, dst)
				return
			}
		}
	}
	log.Printf("[WARN] 未找到默认 policy.yaml，将由程序写入内置默认值")
}

// ============================================================
// Windows 桌面端标准能力装配（PRD 3.9 / 3.10）
// 单实例已在 application.Options.SingleInstance 装配；
// 这里补齐：开机自启、全局快捷键、系统托盘。
// 全部走 Wails v3 内置实现（HKCU Run / RegisterHotKey / Shell_NotifyIcon），
// 不手写 Win32，避免跨版本兼容风险。
// ============================================================

// normalizeAccel 把配置项里的跨平台写法规范化为本机写法。
// 例如 CmdOrCtrl+Shift+P 在 Windows 上应为 Ctrl+Shift+P。
func normalizeAccel(accel string) string {
	a := strings.TrimSpace(accel)
	if a == "" {
		return a
	}
	if runtime.GOOS == "darwin" {
		return strings.Replace(a, "CmdOrCtrl", "Cmd", 1)
	}
	return strings.Replace(a, "CmdOrCtrl", "Ctrl", 1)
}

// accelFallbacks 为一个快捷键组合生成备用组合列表。
// 当首选组合被其它程序占用（RegisterHotKey 返回"已注册"）时依次尝试。
func accelFallbacks(accel string) []string {
	// 拆出最后一个键（主键），前缀为修饰键部分
	idx := strings.LastIndex(accel, "+")
	key := accel
	prefix := ""
	if idx >= 0 {
		key = accel[idx+1:]
		prefix = accel[:idx+1]
	}
	// 依次替换修饰键前缀，主键保持不变
	alts := []string{"Ctrl+Alt+", "Alt+Shift+", "Ctrl+Alt+Shift+", "Ctrl+Shift+Alt+"}
	out := make([]string, 0, len(alts)+1)
	out = append(out, accel)
	for _, a := range alts {
		cand := a + key
		if cand != accel {
			out = append(out, cand)
		}
	}
	_ = prefix
	return out
}

// registerGlobalShortcuts 注册全局快捷键（RegisterHotKey）。
//
//	Ctrl+Shift+P  暂停/恢复（最高优先级，演示/救急）
//	Ctrl+Shift+F  冻结当前帧
//	Ctrl+Shift+Z  撤销最近一次点击（3 秒窗口由 app 层控制）
//
// 组合键来自配置（config.json 的 global_shortcut_*），未配置时用平台默认值。
// 当某个组合被其它程序占用时自动回退到备用组合，确保每个功能最终都有可用快捷键。
//
// 必须在应用启动之后调用：Wails 在应用启动前会把注册请求排入 pending 队列并直接
// 返回 nil，真正的 OS 注册（flushPending）失败时原调用方早已返回，无法回退。
func registerGlobalShortcuts(wailsApp *application.App, svc *ScreenGuardService) {
	registerShortcut := func(name, accel string, cb func()) {
		bind, err := registerWithFallback(wailsApp, accel, cb)
		if err != nil {
			log.Printf("[HotKey] %s 注册失败：%s 及全部备用组合均被占用（%v）", name, accel, err)
			return
		}
		if bind != accel {
			log.Printf("[HotKey] %s: %s 被占用，已回退到 %s", name, accel, bind)
		} else {
			log.Printf("[HotKey] %s: %s", name, bind)
		}
	}

	// 配置驱动：读取 config.json 的 global_shortcut_*，未配置则用平台默认值
	pauseAccel, freezeAccel, undoAccel := svc.Shortcuts()
	registerShortcut("暂停/恢复", normalizeAccel(pauseAccel), func() {
		// 切换暂停/恢复：先尝试恢复，再尝试暂停（幂等由 app 内部保证）
		if err := svc.Resume(); err != nil {
			_ = svc.Pause(10)
		}
	})
	registerShortcut("冻结当前帧", normalizeAccel(freezeAccel), func() {
		svc.FreezeFrame(true)
	})
	registerShortcut("撤销上次点击", normalizeAccel(undoAccel), func() {
		// 撤销最近一次点击（若 app 层支持），否则仅恢复
		svc.UndoLastClick()
	})
}

// registerWithFallback 注册快捷键；被占用时自动尝试备用组合。
// 返回实际绑定成功的组合与错误（全部失败时才返回错误）。
func registerWithFallback(wailsApp *application.App, accel string, cb func()) (string, error) {
	var lastErr error
	for _, cand := range accelFallbacks(accel) {
		lastErr = wailsApp.GlobalShortcut.Register(cand, cb)
		if lastErr == nil {
			return cand, nil
		}
	}
	return "", lastErr
}

func setupDesktopStandardFeatures(wailsApp *application.App, svc *ScreenGuardService, mainWindow application.Window) {
	// ---- 1. 开机自启（HKCU\Software\Microsoft\Windows\CurrentVersion\Run）----
	// 仅当用户显式开启时注册；这里默认开启，符合"装好就后台守护"的产品定位。
	if err := wailsApp.Autostart.Enable(); err != nil {
		log.Printf("[AutoStart] 注册开机自启失败（非致命）: %v", err)
	} else {
		log.Println("[AutoStart] 已注册开机自启（HKCU Run）")
	}

	// ---- 3. 系统托盘（Shell_NotifyIcon）----
	// 图标颜色语义见 internal/platform.TrayColor：
	//   blue=自动正常  gray=观察/暂停  red=异常/未授权  yellow=刚点过
	tray := wailsApp.SystemTray.New()
	tray.SetTooltip("屏净 ScreenGuard")
	tray.SetMenu(buildTrayMenu(svc, mainWindow))
	// 默认蓝色图标（自动模式正常）；无图标资源时用空字节，Wails 会回退系统图标
	tray.SetIcon(loadTrayIcon(platform.TrayBlue))
	log.Println("[Tray] 系统托盘已创建")
}

// buildTrayMenu 构造托盘右键菜单。回调复用 ScreenGuardService 已有方法。
func buildTrayMenu(svc *ScreenGuardService, mainWindow application.Window) *application.Menu {
	menu := application.NewMenu()
	menu.Add("暂停 10 分钟").OnClick(func(*application.Context) { _ = svc.Pause(10) })
	menu.Add("暂停 30 分钟").OnClick(func(*application.Context) { _ = svc.Pause(30) })
	menu.Add("暂停 60 分钟").OnClick(func(*application.Context) { _ = svc.Pause(60) })
	menu.AddSeparator()
	menu.Add("恢复").OnClick(func(*application.Context) { _ = svc.Resume() })
	menu.AddSeparator()
	menu.Add("打开主窗口").OnClick(func(*application.Context) {
		if mainWindow != nil {
			mainWindow.Show()
			mainWindow.Focus()
		}
	})
	menu.Add("运行自检").OnClick(func(*application.Context) { _ = svc.RunSelfTest() })
	menu.AddSeparator()
	menu.Add("刚才那个不该点").OnClick(func(*application.Context) { svc.UndoLastClick() })
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(*application.Context) { svc.QuitApp() })
	return menu
}

// loadTrayIcon 加载托盘图标字节（见 build 目录下的 png 资源）。
//
// 路径不能只相对当前工作目录：程序可能被从任意目录启动（分发形态下 CWD 往往
// 与 exe 所在目录不同），一旦找不到就会把空字节交给 systray，
// 表现为 "failed to create systray icon: invalid file format"。
// 因此这里以 exe 所在目录为主、CWD 为辅逐层向上查找，并校验 PNG 文件头，
// 避免把非 PNG 数据传给托盘。全部失败才回退空字节（Wails 使用系统默认图标）。
func loadTrayIcon(color platform.TrayColor) []byte {
	name := "tray_" + color.String() + ".png"

	var bases []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		// 分发形态：图标与 exe 同级；开发形态：exe 在 build\windows 下，图标在 build\
		for i := 0; i < 3; i++ {
			bases = append(bases, dir)
			dir = filepath.Dir(dir)
		}
	}
	if wd, err := os.Getwd(); err == nil {
		bases = append(bases, wd, filepath.Dir(wd))
	}

	rels := []string{
		name,
		filepath.Join("build", name),
		filepath.Join("resources", name),
		filepath.Join("build", "appicon.png"),
		"appicon.png",
	}

	for _, base := range bases {
		for _, rel := range rels {
			p := filepath.Join(base, rel)
			data, err := os.ReadFile(p)
			if err != nil || len(data) == 0 {
				continue
			}
			if !isPNG(data) {
				continue
			}
			return data
		}
	}
	return []byte{}
}

// isPNG 校验 PNG 文件签名，防止把占位/损坏文件传给系统托盘。
func isPNG(data []byte) bool {
	return len(data) >= 8 &&
		data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' &&
		data[4] == 0x0d && data[5] == 0x0a && data[6] == 0x1a && data[7] == 0x0a
}
