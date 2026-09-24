//go:build windows

package click

// ============================================================
// Windows 点击实现 — SendInput（纯 Go syscall，无 cgo）
//
// 取舍说明：
//   1. 必带 MOUSEEVENTF_VIRTUALDESK —— 否则归一化坐标只映射到主显示器，
//      多显示器场景下副屏点击会整体偏移（抓屏侧取的是虚拟屏幕）。
//   2. 不做光标位置还原 —— 与 macOS 的 CGEventPost 实现保持行为一致，
//      避免"点了之后光标乱跳"与"还原时覆盖用户刚做的移动"两种干扰叠加。
//   3. 消失验证判据与 macOS 完全一致（整屏帧差 >= 0.005），
//      保证双平台日志口径可横向对比（PRD 4.3.3 / 红线 B6）。
//
// 已知限制（UIPI）：完整性级别低于目标窗口时 SendInput 会被静默丢弃。
//   即以普通权限运行时无法点击管理员权限窗口，这由禁区检测在决策链第 1 步拦掉。
// ============================================================

import (
	"fmt"
	"image"
	"syscall"
	"time"
	"unsafe"

	"screenguard/internal/capture"
	"screenguard/internal/platform/win"
)

const (
	inputMouse = 0

	mouseEventFMove        = 0x0001
	mouseEventFLeftDown    = 0x0002
	mouseEventFLeftUp      = 0x0004
	mouseEventFAbsolute    = 0x8000
	mouseEventFVirtualDesk = 0x4000 // 映射到整个虚拟桌面（多显示器必需）

	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
)

var (
	modUser32 = syscall.NewLazyDLL("user32.dll")

	procSendInput       = modUser32.NewProc("SendInput")
	procGetSystemMetric = modUser32.NewProc("GetSystemMetrics")
)

// mouseInput MOUSEINPUT
// x64 布局：dx(0) dy(4) mouseData(8) dwFlags(12) timestamp(16) pad(20) dwExtraInfo(24) → 32 字节
type mouseInput struct {
	dx          int32
	dy          int32
	mouseData   uint32
	dwFlags     uint32
	timestamp   uint32
	dwExtraInfo uintptr
}

// input INPUT：typ(0) + pad(4) + union(8..40) → 40 字节（x64）
// Go 会因 dwExtraInfo 的 uintptr 把 mi 自动对齐到 8 字节偏移，与 Windows 布局一致
type input struct {
	typ uint32
	mi  mouseInput
}

// SendInputClicker Windows 点击器
type SendInputClicker struct {
	capturer      capture.Capturer // 用于消失验证时复截
	lastVanishROI float64          // 最近一次消失验证的帧差
}

// NewSendInputClicker 创建 Windows 点击器
func NewSendInputClicker(cap capture.Capturer) Clicker {
	return &SendInputClicker{capturer: cap}
}

// NewPlatformClicker 跨平台工厂（与 darwin 侧同名函数对齐，供 main 统一调用）
func NewPlatformClicker(cap capture.Capturer) Clicker {
	return NewSendInputClicker(cap)
}

// Click 在指定物理像素坐标执行点击。
// scaleFactor 在 Windows 侧不使用：进程已在启动时声明 DPI aware（capture.InitDPIAware），
// 系统度量与 SendInput 坐标同为物理像素，无需再做缩放换算（PRD 4.3.5）。
func (c *SendInputClicker) Click(x, y float64, scaleFactor float64) error {
	// 自激防护兜底：若点击目标落在自身窗口内（SetWindowDisplayAffinity 失败时），
	// 绝不执行点击，避免引擎点到自己的关闭按钮导致窗口收托盘/退出。
	if win.IsPointInSelfWindow(int(x), int(y)) {
		return ErrSelfWindow
	}

	nx, ny, err := normalizeToVirtualDesk(x, y)
	if err != nil {
		return err
	}

	// 先移动：部分弹窗的 × 在 hover 态才渲染，直接 down 会点空
	if err := sendMouseInput(mouseEventFMove, nx, ny); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)

	if err := sendMouseInput(mouseEventFLeftDown, nx, ny); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)

	return sendMouseInput(mouseEventFLeftUp, nx, ny)
}

// VerifyVanish 真实消失验证（红线 B6 / PRD 4.3.3）
// 判据与 macOS 实现完全一致：复截一帧做整屏帧差
//
//	diff < 0.005 → 屏幕几乎没变 → 点击无效 → vanished=false
//	diff >= 0.005 → 有实质变化（弹窗消失）→ vanished=true
func (c *SendInputClicker) VerifyVanish(before interface{}) (bool, error) {
	if c.capturer == nil {
		return false, fmt.Errorf("无抓屏器，无法验证")
	}

	beforeImg, ok := before.(image.Image)
	if !ok || beforeImg == nil {
		return false, fmt.Errorf("点击前帧类型无效")
	}

	afterImg, _, err := c.capturer.Capture()
	if err != nil {
		return false, fmt.Errorf("验证截图失败: %w", err)
	}

	diff, err := capture.FrameDiff(beforeImg, afterImg, 10)
	if err != nil {
		return false, err
	}
	c.lastVanishROI = diff

	return diff >= 0.005, nil
}

// LastVanishDiff 返回最近一次验证的帧差值（诊断用）
func (c *SendInputClicker) LastVanishDiff() float64 { return c.lastVanishROI }

// IsAuthorized Windows 无辅助功能授权门禁，恒为可用。
// 真实限制来自 UIPI（完整性级别）与 UAC 安全桌面，由禁区检测处理。
func (c *SendInputClicker) IsAuthorized() bool { return true }

// RequestPermission Windows 侧无对应授权申请入口；
// UIPI 限制只能通过以相同/更高完整性级别运行解决，返回当前可用状态。
func (c *SendInputClicker) RequestPermission() bool { return true }

// MoveTo 移动鼠标到指定位置（不点击）
func (c *SendInputClicker) MoveTo(x, y float64, scaleFactor float64) {
	nx, ny, err := normalizeToVirtualDesk(x, y)
	if err != nil {
		return
	}
	sendMouseInput(mouseEventFMove, nx, ny)
}

// normalizeToVirtualDesk 虚拟屏幕图像像素坐标 → SendInput 归一化坐标 (0..65535)
// 抓屏返回的是虚拟屏幕图像，像素 (0,0) 即虚拟桌面左上角，
// 因此归一化时虚拟屏 origin 自然抵消：n = px * 65535 / vw
func normalizeToVirtualDesk(x, y float64) (int32, int32, error) {
	vw := sysMetric(smCXVirtualScreen)
	vh := sysMetric(smCYVirtualScreen)
	if vw <= 0 || vh <= 0 {
		return 0, 0, fmt.Errorf("无法获取虚拟屏幕尺寸")
	}

	nx := int32(x * 65535.0 / float64(vw))
	ny := int32(y * 65535.0 / float64(vh))

	// 夹紧：越界坐标会被系统拒绝或落到错误位置
	if nx < 0 {
		nx = 0
	} else if nx > 65535 {
		nx = 65535
	}
	if ny < 0 {
		ny = 0
	} else if ny > 65535 {
		ny = 65535
	}
	return nx, ny, nil
}

// sendMouseInput 发送单个鼠标输入事件
func sendMouseInput(flags uint32, nx, ny int32) error {
	var in input
	in.typ = inputMouse
	in.mi.dx = nx
	in.mi.dy = ny
	in.mi.dwFlags = flags | mouseEventFAbsolute | mouseEventFVirtualDesk

	ret, _, errNo := procSendInput.Call(
		1,
		uintptr(unsafe.Pointer(&in)),
		unsafe.Sizeof(in),
	)
	if ret != 1 {
		return fmt.Errorf("SendInput 失败 (flags=0x%X, errno=%v)", flags, errNo)
	}
	return nil
}

func sysMetric(index int) int {
	r, _, _ := procGetSystemMetric.Call(uintptr(index))
	return int(int32(r))
}
