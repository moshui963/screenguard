//go:build windows

package win

// ============================================================
// 用户空闲检测 — PRD 红线 P1-3
// 对应 macOS 的 CGEventSourceSecondsSinceLastEventType
// 引擎用它做"用户正在操作就不要点"的门控（< 1.5s → blocked_user_active）
// ============================================================

import (
	"syscall"
	"unsafe"
)

// lastInputInfo LASTINPUTINFO
type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

var (
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")
	modUser32   = syscall.NewLazyDLL("user32.dll")

	procGetTickCount     = modKernel32.NewProc("GetTickCount")
	procGetLastInputInfo = modUser32.NewProc("GetLastInputInfo")
)

// SecondsSinceLastUserInput 距上次真实用户输入（键盘/鼠标）的秒数。
//
// 取不到时返回 0：引擎对 0 的处理是"视作用户刚输入过，不点击"，
// 即故障导向保守（PRD 附录A 约束3：任何不确定都倒向不点）。
func SecondsSinceLastUserInput() float64 {
	var lii lastInputInfo
	lii.cbSize = uint32(unsafe.Sizeof(lii))

	ret, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&lii)))
	if ret == 0 || lii.dwTime == 0 {
		return 0
	}

	now := getTickCount()
	// uint32 毫秒计数约 49.7 天回绕；无符号减法在回绕处依然给出正确间隔
	elapsedMs := now - lii.dwTime
	return float64(elapsedMs) / 1000.0
}

func getTickCount() uint32 {
	r, _, _ := procGetTickCount.Call()
	return uint32(r)
}
