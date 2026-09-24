package platform

import "strings"

// ============================================================
// 托盘 + 全局快捷键 + 桌面叠加层 + 禁区状态
// PRD 3.9 / 3.10 / 4.3.1
// ============================================================

// ForbiddenZoneState 禁区状态快照（双平台共享，无 build tag）
// PRD 4.3.1 决策链第 1 步：命中任一禁区即不执行点击。
type ForbiddenZoneState struct {
	Fullscreen     bool // 全屏应用（游戏 / 放映）
	Locked         bool // 锁屏 / 屏保
	RemoteDesktop  bool // 远程桌面（本机被控，或前台为远程桌面客户端）
	SecureDesktop  bool // UAC 安全桌面 / 系统级安装向导
	LowBattery     bool // 低电（< 20%）→ 暂停检测
	OnBattery      bool // 电池供电（非 AC）→ 自动降帧，本身不是禁区
	BatteryPercent int  // 255 = 未知
}

// Any 是否命中任一禁区（命中即不应执行点击）
// OnBattery 不计入：PRD 第6章要求电池模式"自动降帧"而非完全停手，
// 只有 LowBattery（< 20%）才暂停检测。
func (s ForbiddenZoneState) Any() bool {
	return s.Fullscreen || s.Locked || s.RemoteDesktop || s.SecureDesktop || s.LowBattery
}

// Reason 返回命中的禁区原因，写入事件日志 / preempted_cause
func (s ForbiddenZoneState) Reason() string {
	switch {
	case s.Locked:
		return "locked"
	case s.SecureDesktop:
		return "secure_desktop"
	case s.RemoteDesktop:
		return "remote_desktop"
	case s.Fullscreen:
		return "fullscreen_app"
	case s.LowBattery:
		return "low_battery"
	default:
		return ""
	}
}

// ============================================================
// 禁区判定 — 进程名 heuristic（双平台共享纯逻辑）
// macOS / Windows 都用"前台进程名清单"做纵深防御；
// 纯函数便于在交叉编译环境跑单测，避免各平台重复实现。
// ============================================================

// 远程桌面客户端 bundle/进程名关键字（本机被控或前台为远程桌面客户端）
var remoteDesktopClientKeywords = []string{
	"rdc", "teamviewer", "anydesk", "todesk", "sunlogin", "rustdesk", "parsec", "zoom",
}

// 系统安装器 / 特权界面关键字
var systemInstallerKeywords = []string{
	"installer", "pkgutil", "systempreferences", "consent", "msiexec", "trustedinstaller",
}

// 屏保关键字
var screenSaverKeywords = []string{
	"screensaver",
}

// MatchForbiddenByProcessName 按进程名关键字匹配禁区类型。
// name 为小写化的进程名 / bundle id。返回命中的禁区字段（不修改传入的 s 之外状态）。
func MatchForbiddenByProcessName(s *ForbiddenZoneState, name string) {
	name = strings.ToLower(name)
	for _, k := range remoteDesktopClientKeywords {
		if strings.Contains(name, k) {
			s.RemoteDesktop = true
			break
		}
	}
	for _, k := range systemInstallerKeywords {
		if strings.Contains(name, k) {
			s.SecureDesktop = true
			break
		}
	}
	for _, k := range screenSaverKeywords {
		if strings.Contains(name, k) {
			s.Locked = true
			break
		}
	}
}

// TrayMenuItem 托盘菜单项
type TrayMenuItem struct {
	Label     string
	OnClick   func()
	Disabled  bool
	Separator bool
}

// TrayConfig 托盘配置
type TrayConfig struct {
	Title      string
	IconGray   string // 观察模式/暂停
	IconBlue   string // 自动模式正常
	IconRed    string // 异常/未授权
	IconYellow string // 刚点过
	Items      []TrayMenuItem
}

// GlobalShortcut 全局快捷键
type GlobalShortcut struct {
	Pause  string // 暂停/恢复（最高优先级，演示场景救急）
	Freeze string // 冻结当前帧
	Undo   string // "刚才那个不该点" — 3秒窗口
}

// DefaultTrayConfig 默认托盘配置
func DefaultTrayConfig() *TrayConfig {
	return &TrayConfig{
		Title: "屏净 ScreenGuard",
		Items: []TrayMenuItem{
			{Label: "暂停 10 分钟", OnClick: nil},
			{Label: "暂停 30 分钟", OnClick: nil},
			{Label: "暂停 60 分钟", OnClick: nil},
			{Separator: true},
			{Label: "恢复", OnClick: nil, Disabled: true},
			{Separator: true},
			{Label: "打开主窗口", OnClick: nil},
			{Label: "运行自检", OnClick: nil},
			{Separator: true},
			{Label: "刚才那个不该点", OnClick: nil, Disabled: true}, // 点击后 3 秒内可用
			{Separator: true},
			{Label: "退出", OnClick: nil},
		},
	}
}

// DefaultGlobalShortcuts 默认全局快捷键
func DefaultGlobalShortcuts() *GlobalShortcut {
	return &GlobalShortcut{
		Pause:  "CmdOrCtrl+Shift+P",
		Freeze: "CmdOrCtrl+Shift+F",
		Undo:   "CmdOrCtrl+Shift+Z",
	}
}

// TrayColor 托盘图标颜色语义
type TrayColor int

const (
	TrayGray   TrayColor = iota // 观察/暂停
	TrayBlue                    // 自动正常
	TrayRed                     // 异常/未授权
	TrayYellow                  // 刚点过一次
)

// String 颜色名称
func (c TrayColor) String() string {
	switch c {
	case TrayGray:
		return "gray"
	case TrayBlue:
		return "blue"
	case TrayRed:
		return "red"
	case TrayYellow:
		return "yellow"
	default:
		return "unknown"
	}
}

// ============================================================
// 桌面叠加层 Overlay — PRD 3.10（可选，默认关闭）
// 自激防护硬约束：excludedWindows 排除自身全部窗口
// ============================================================

// OverlayConfig 叠加层配置
type OverlayConfig struct {
	Enabled bool
	// macOS: NSPanel + nonactivatingPanel + ignoresMouseEvents
	// 不抢焦点、不进 Mission Control、不出现在任务切换器
	NonActivating   bool
	IgnoresMouse    bool
	ExcludedWindows []int32 // 自激防护：排除自身窗口
}

// DefaultOverlayConfig 默认叠加层配置（关闭）
func DefaultOverlayConfig() *OverlayConfig {
	return &OverlayConfig{
		Enabled:         false,
		NonActivating:   true,
		IgnoresMouse:    true,
		ExcludedWindows: nil,
	}
}
