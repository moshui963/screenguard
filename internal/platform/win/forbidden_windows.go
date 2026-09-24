//go:build windows

package win

// ============================================================
// 禁区检测 — PRD 4.3.1 决策链第 1 步 / 3.4.2 禁区清单 / 第6章 系统限制
//
// Windows 没有 macOS 的 TCC 授权门禁，"能不能点"的安全边界全部落在这里：
//   全屏应用（游戏/放映）· 锁屏屏保 · 远程桌面 · UAC 安全桌面与安装向导 · 低电
// 命中任一禁区即不执行点击（PRD 状态机：未授权/已暂停态禁止一切检测与点击）。
// ============================================================

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"screenguard/internal/platform"
)

const (
	spiGetScreenSaverRunning = 0x0072
	smRemoteSession          = 0x1000

	monitorDefaultToNearest = 0x00000002

	processQueryLimitedInformation = 0x1000
	uoiName                        = 2
)

// 以下 proc 变量在本文件内定义；modUser32 / modKernel32 定义在 idle_windows.go（同包共享）
var (
	procGetForegroundWindow      = modUser32.NewProc("GetForegroundWindow")
	procGetShellWindow           = modUser32.NewProc("GetShellWindow")
	procGetDesktopWindow         = modUser32.NewProc("GetDesktopWindow")
	procGetWindowRect            = modUser32.NewProc("GetWindowRect")
	procMonitorFromWindow        = modUser32.NewProc("MonitorFromWindow")
	procGetMonitorInfo           = modUser32.NewProc("GetMonitorInfoW")
	procSystemParametersInfo     = modUser32.NewProc("SystemParametersInfoW")
	procGetSystemMetrics         = modUser32.NewProc("GetSystemMetrics")
	procGetWindowThreadProcessId = modUser32.NewProc("GetWindowThreadProcessId")
	procGetThreadDesktop         = modUser32.NewProc("GetThreadDesktop")
	procGetUserObjectInformation = modUser32.NewProc("GetUserObjectInformationW")

	procGetSystemPowerStatus      = modKernel32.NewProc("GetSystemPowerStatus")
	procOpenProcess               = modKernel32.NewProc("OpenProcess")
	procCloseHandle               = modKernel32.NewProc("CloseHandle")
	procQueryFullProcessImageName = modKernel32.NewProc("QueryFullProcessImageNameW")
	procGetCurrentThreadId        = modKernel32.NewProc("GetCurrentThreadId")
)

// rect RECT
type rect struct {
	left, top, right, bottom int32
}

// monitorInfo MONITORINFO
type monitorInfo struct {
	cbSize    uint32
	rcMonitor rect
	rcWork    rect
	dwFlags   uint32
}

// systemPowerStatus SYSTEM_POWER_STATUS
type systemPowerStatus struct {
	acLineStatus        byte
	batteryFlag         byte
	batteryLifePercent  byte
	systemStatusFlag    byte
	batteryLifeTime     uint32
	batteryFullLifeTime uint32
}

// ForbiddenZoneState 类型定义见 internal/platform（双平台共享），
// 这里直接使用 platform.ForbiddenZoneState。

// CheckForbiddenZones 检测当前是否处于禁区场景
func CheckForbiddenZones() platform.ForbiddenZoneState {
	var s platform.ForbiddenZoneState

	s.Fullscreen = isFullscreenApp()
	s.Locked = isScreenSaverRunning() || isLockScreenForeground()
	s.RemoteDesktop = isRemoteSession() || isRemoteDesktopClientForeground()
	s.SecureDesktop = isSecureDesktop() || isSystemInstallerForeground()

	ac, pct := powerStatus()
	s.OnBattery = ac == 0
	s.BatteryPercent = int(pct)
	s.LowBattery = pct != 255 && int(pct) < 20

	return s
}

// isFullscreenApp 前台窗口铺满所在显示器，且不是桌面/外壳窗口
func isFullscreenApp() bool {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return false // 无前台窗口多为锁屏或切换中，交给 Locked 判定，避免误判全屏
	}
	shell, _, _ := procGetShellWindow.Call()
	desktop, _, _ := procGetDesktopWindow.Call()
	if hwnd == shell || hwnd == desktop {
		return false
	}

	var r rect
	if ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ret == 0 {
		return false
	}

	hMon, _, _ := procMonitorFromWindow.Call(hwnd, uintptr(monitorDefaultToNearest))
	if hMon == 0 {
		return false
	}
	var mi monitorInfo
	mi.cbSize = uint32(unsafe.Sizeof(mi))
	if ret, _, _ := procGetMonitorInfo.Call(hMon, uintptr(unsafe.Pointer(&mi))); ret == 0 {
		return false
	}

	return r.left <= mi.rcMonitor.left && r.top <= mi.rcMonitor.top &&
		r.right >= mi.rcMonitor.right && r.bottom >= mi.rcMonitor.bottom
}

func isScreenSaverRunning() bool {
	var running int32
	ret, _, _ := procSystemParametersInfo.Call(
		uintptr(spiGetScreenSaverRunning), 0, uintptr(unsafe.Pointer(&running)), 0)
	return ret != 0 && running != 0
}

func isRemoteSession() bool {
	r, _, _ := procGetSystemMetrics.Call(uintptr(smRemoteSession))
	return r != 0
}

// isSecureDesktop 当前线程所在桌面不是交互桌面（Default）即为安全桌面。
// 安全桌面（Winlogon）下抓屏与输入注入均不可用，必须停手。
func isSecureDesktop() bool {
	tid, _, _ := procGetCurrentThreadId.Call()
	hDesk, _, _ := procGetThreadDesktop.Call(tid)
	if hDesk == 0 {
		return false
	}
	var buf [256]uint16
	var needed uint32
	ret, _, _ := procGetUserObjectInformation.Call(
		hDesk, uintptr(uoiName),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)),
		uintptr(unsafe.Pointer(&needed)),
	)
	if ret == 0 {
		return false
	}
	name := syscall.UTF16ToString(buf[:])
	return name != "" && !strings.EqualFold(name, "Default")
}

// remoteDesktopClients 远程桌面客户端进程名。
// 前台是这类程序时不介入，避免自动点击作用到远端机器（PRD 3.4.2 禁区清单）。
var remoteDesktopClients = []string{
	"mstsc.exe", "teamviewer.exe", "anydesk.exe", "todesk.exe",
	"sunloginclient.exe", "rustdesk.exe", "parsec.exe",
}

// systemInstallers UAC 与系统级安装向导进程名
var systemInstallers = []string{
	"consent.exe", "msiexec.exe", "trustedinstaller.exe",
}

func isRemoteDesktopClientForeground() bool {
	return matchForegroundProcess(remoteDesktopClients)
}

func isSystemInstallerForeground() bool {
	return matchForegroundProcess(systemInstallers)
}

func isLockScreenForeground() bool {
	name := foregroundProcessName()
	switch name {
	case "lockapp.exe", "logonui.exe":
		return true
	}
	return false
}

func matchForegroundProcess(candidates []string) bool {
	name := foregroundProcessName()
	if name == "" {
		return false
	}
	for _, c := range candidates {
		if name == c {
			return true
		}
	}
	return false
}

// foregroundProcessName 取前台窗口所属进程的可执行文件名（小写）。
// 失败返回空串 —— 调用方按"未命中"处理，避免把取不到信息当成命中禁区而误停。
func foregroundProcessName() string {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}

	hProc, _, _ := procOpenProcess.Call(uintptr(processQueryLimitedInformation), 0, uintptr(pid))
	if hProc == 0 {
		return ""
	}
	defer procCloseHandle.Call(hProc)

	buf := make([]uint16, 260)
	size := uint32(len(buf))
	ret, _, _ := procQueryFullProcessImageName.Call(
		hProc, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ret == 0 || size == 0 {
		return ""
	}
	return strings.ToLower(filepath.Base(syscall.UTF16ToString(buf[:size])))
}

// powerStatus 返回 (供电状态, 电量百分比)。
// ACLineStatus: 0=电池 1=AC 255=未知；电量 255=未知。
// 取不到时按"接电 + 未知"返回，不做低电拦截（故障导向继续工作）。
func powerStatus() (acLine byte, percent byte) {
	var ps systemPowerStatus
	ret, _, _ := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&ps)))
	if ret == 0 {
		return 1, 255
	}
	return ps.acLineStatus, ps.batteryLifePercent
}
