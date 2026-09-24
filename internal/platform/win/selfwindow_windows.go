//go:build windows

package win

import (
	"syscall"
	"unsafe"
)

// procEnumWindows 枚举当前所有顶层窗口（自激防护兜底用）。
var procEnumWindows = modUser32.NewProc("EnumWindows")

// IsPointInSelfWindow 判断屏幕物理坐标 (x,y) 是否落在当前进程任一顶层窗口矩形内。
// 用于"自激点击"兜底：即便 SetWindowDisplayAffinity 失败（旧系统不支持），
// 也绝不对自身窗口（主窗口/调试台/探针）执行点击，避免引擎点到自己的关闭按钮
// 导致窗口收托盘/退出。
//
// 坐标系：进程已在启动时声明 DPI aware，GetWindowRect 返回物理像素，
// 与抓屏/点击所用的虚拟屏幕物理像素一致，可直接比较。
func IsPointInSelfWindow(x, y int) bool {
	pid := uint32(syscall.Getpid())
	var hit bool
	cb := syscall.NewCallback(func(hwnd, lParam uintptr) uintptr {
		var wpid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&wpid)))
		if wpid != pid {
			return 1 // 非本进程窗口，继续枚举
		}
		var r rect
		if ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ret == 0 {
			return 1
		}
		// 跳过零尺寸/最小化窗口（无实际可点区域）
		if r.right <= r.left || r.bottom <= r.top {
			return 1
		}
		if int32(x) >= r.left && int32(x) <= r.right &&
			int32(y) >= r.top && int32(y) <= r.bottom {
			hit = true
			return 0 // 命中，停止枚举
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return hit
}
