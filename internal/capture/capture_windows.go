//go:build windows

package capture

// ============================================================
// Windows 抓屏实现 — GDI BitBlt（纯 Go syscall，无 cgo、无第三方依赖）
//
// 选型说明（与 PRD 附录A 原定的 "C# WGC sidecar" 方案不同）：
//   本项目要求单一语言技术栈、避免过度复杂。GDI BitBlt 用纯 Go syscall 即可完成，
//   在 PRD 4.3.6 要求的 2.5~3 fps 下性能充足（单次全屏 BitBlt 约 10~30ms），
//   且 Windows 10 2004+ 的 SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE)
//   能同时满足 PRD 4.3.7 的自激防护硬约束，因此无需引入 C# 进程。
//
// 覆盖范围：虚拟屏幕（SM_*VIRTUALSCREEN），包含所有显示器。
//   副显示器 origin 可为负值，需正确处理（PRD 4.3.5）。
// ============================================================

import (
	"fmt"
	"image"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Windows GDI / USER 常量
const (
	srcCopy    = 0x00CC0020 // SRCCOPY
	captureBlt = 0x40000000 // CAPTUREBLT：包含被分层窗口覆盖的区域

	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	biRGB        = 0 // BI_RGB
	dibRGBColors = 0 // DIB_RGB_COLORS

	// WDA_EXCLUDEFROMCAPTURE：窗口对抓屏不可见（GDI BitBlt 与 WGC 均生效）
	// 这是 PRD 4.3.7 自激防护在 Windows 侧的实现基础
	wdaExcludeFromCapture = 0x00000011
)

var (
	modUser32 = syscall.NewLazyDLL("user32.dll")
	modGdi32  = syscall.NewLazyDLL("gdi32.dll")

	procGetDC                    = modUser32.NewProc("GetDC")
	procReleaseDC                = modUser32.NewProc("ReleaseDC")
	procGetSystemMetrics         = modUser32.NewProc("GetSystemMetrics")
	procSetProcessDPIAware       = modUser32.NewProc("SetProcessDPIAware")
	procGetDpiForSystem          = modUser32.NewProc("GetDpiForSystem")
	procSetWindowDisplayAffinity = modUser32.NewProc("SetWindowDisplayAffinity")
	procEnumWindows              = modUser32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = modUser32.NewProc("GetWindowThreadProcessId")

	procCreateCompatibleDC     = modGdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = modGdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = modGdi32.NewProc("SelectObject")
	procBitBlt                 = modGdi32.NewProc("BitBlt")
	procDeleteObject           = modGdi32.NewProc("DeleteObject")
	procDeleteDC               = modGdi32.NewProc("DeleteDC")
	procGetDIBits              = modGdi32.NewProc("GetDIBits")
)

// bitmapInfoHeader BITMAPINFOHEADER
type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

// bitmapInfo BITMAPINFO（32bpp 无需调色板）
type bitmapInfo struct {
	bmiHeader bitmapInfoHeader
}

// GDICapturer Windows GDI 抓屏器
type GDICapturer struct {
	mu          sync.Mutex
	originX     int     // 虚拟屏幕左上角 X（可为负）
	originY     int     // 虚拟屏幕左上角 Y（可为负）
	width       int     // 虚拟屏幕宽（物理像素）
	height      int     // 虚拟屏幕高（物理像素）
	scaleFactor float64 // DPI 缩放（96 = 1.0）

	hdcMem      uintptr // 复用的内存 DC
	hbm         uintptr // 复用的位图
	oldObj      uintptr // SelectObject 返回的旧对象
	buf         []byte  // 复用像素缓冲（BGRA）
	initialized bool
}

// NewGDICapturer 创建 Windows GDI 抓屏器
func NewGDICapturer() *GDICapturer {
	InitDPIAware()
	return &GDICapturer{
		scaleFactor: systemScaleFactor(),
	}
}

// InitDPIAware 声明进程为 DPI 感知，使 GetSystemMetrics 返回物理像素。
// 必须在创建任何窗口之前调用（main 最开头），否则缩放取值会偏（PRD 4.3.5）。
// 幂等，可重复调用。
func InitDPIAware() {
	if err := procSetProcessDPIAware.Find(); err == nil {
		procSetProcessDPIAware.Call()
	}
}

// systemScaleFactor 查询系统 DPI 缩放；老系统回退 1.0
func systemScaleFactor() float64 {
	if err := procGetDpiForSystem.Find(); err == nil {
		dpi, _, _ := procGetDpiForSystem.Call()
		if dpi > 0 {
			return float64(dpi) / 96.0
		}
	}
	return 1.0
}

func sysMetric(index int) int {
	r, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int(int32(r))
}

// Capture 截取虚拟全屏，返回物理像素 image（PRD 4.3.5：全链路统一物理像素）
func (c *GDICapturer) Capture() (image.Image, int, error) {
	start := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	hdcScreen, _, _ := procGetDC.Call(0)
	if hdcScreen == 0 {
		return nil, 0, fmt.Errorf("GetDC(0) 失败")
	}
	defer procReleaseDC.Call(0, hdcScreen)

	if err := c.ensureResources(hdcScreen); err != nil {
		return nil, 0, err
	}

	// 虚拟屏幕 origin 可能为负（副屏在主屏左侧/上方），按 int32 传递
	ret, _, _ := procBitBlt.Call(
		c.hdcMem, 0, 0, uintptr(c.width), uintptr(c.height),
		hdcScreen, uintptr(int32(c.originX)), uintptr(int32(c.originY)),
		uintptr(srcCopy|captureBlt),
	)
	if ret == 0 {
		return nil, 0, fmt.Errorf("BitBlt 失败")
	}

	var bi bitmapInfo
	bi.bmiHeader.biSize = uint32(unsafe.Sizeof(bi.bmiHeader))
	bi.bmiHeader.biWidth = int32(c.width)
	bi.bmiHeader.biHeight = -int32(c.height) // 负值 = top-down，省一次翻转
	bi.bmiHeader.biPlanes = 1
	bi.bmiHeader.biBitCount = 32
	bi.bmiHeader.biCompression = biRGB

	lines, _, _ := procGetDIBits.Call(
		c.hdcMem, c.hbm, 0, uintptr(c.height),
		uintptr(unsafe.Pointer(&c.buf[0])),
		uintptr(unsafe.Pointer(&bi)),
		uintptr(dibRGBColors),
	)
	if lines == 0 {
		return nil, 0, fmt.Errorf("GetDIBits 失败")
	}

	// BGRA → RGBA，并补 alpha（GDI 返回的 alpha 未定义）
	img := image.NewRGBA(image.Rect(0, 0, c.width, c.height))
	copy(img.Pix, c.buf)
	for i := 0; i+3 < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+2] = img.Pix[i+2], img.Pix[i]
		img.Pix[i+3] = 0xFF
	}

	return img, int(time.Since(start).Milliseconds()), nil
}

// ensureResources 按需（重新）创建 DC 与位图；分辨率变化会自动重建
func (c *GDICapturer) ensureResources(hdcScreen uintptr) error {
	vw := sysMetric(smCXVirtualScreen)
	vh := sysMetric(smCYVirtualScreen)
	if vw <= 0 || vh <= 0 {
		return fmt.Errorf("无法获取虚拟屏幕尺寸")
	}
	c.originX = int(int32(sysMetric(smXVirtualScreen)))
	c.originY = int(int32(sysMetric(smYVirtualScreen)))

	if c.initialized && c.width == vw && c.height == vh {
		return nil
	}

	c.releaseResourcesLocked()

	hdcMem, _, _ := procCreateCompatibleDC.Call(hdcScreen)
	if hdcMem == 0 {
		return fmt.Errorf("CreateCompatibleDC 失败")
	}
	hbm, _, _ := procCreateCompatibleBitmap.Call(hdcScreen, uintptr(vw), uintptr(vh))
	if hbm == 0 {
		procDeleteDC.Call(hdcMem)
		return fmt.Errorf("CreateCompatibleBitmap 失败")
	}
	old, _, _ := procSelectObject.Call(hdcMem, hbm)
	if old == 0 {
		procDeleteObject.Call(hbm)
		procDeleteDC.Call(hdcMem)
		return fmt.Errorf("SelectObject 失败")
	}

	c.hdcMem, c.hbm, c.oldObj = hdcMem, hbm, old
	c.width, c.height = vw, vh
	c.buf = make([]byte, vw*vh*4)
	c.initialized = true
	return nil
}

// releaseResourcesLocked 释放 GDI 资源（调用方需持锁）
func (c *GDICapturer) releaseResourcesLocked() {
	if c.hdcMem != 0 {
		if c.oldObj != 0 {
			procSelectObject.Call(c.hdcMem, c.oldObj)
		}
		procDeleteDC.Call(c.hdcMem)
	}
	if c.hbm != 0 {
		procDeleteObject.Call(c.hbm)
	}
	c.hdcMem, c.hbm, c.oldObj = 0, 0, 0
	c.initialized = false
}

// Close 释放 GDI 资源（进程退出前调用）
func (c *GDICapturer) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releaseResourcesLocked()
}

// ScreenSize 返回虚拟屏幕物理像素尺寸与 DPI 缩放
func (c *GDICapturer) ScreenSize() (int, int, float64, error) {
	vw := sysMetric(smCXVirtualScreen)
	vh := sysMetric(smCYVirtualScreen)
	if vw <= 0 || vh <= 0 {
		return 0, 0, 1.0, fmt.Errorf("无法获取虚拟屏幕尺寸")
	}
	return vw, vh, c.scaleFactor, nil
}

// VirtualOrigin 虚拟屏幕左上角坐标（可为负）。
// 屏幕像素坐标 = 虚拟屏幕坐标 - origin，供 SendInput 绝对坐标换算使用。
func (c *GDICapturer) VirtualOrigin() (x, y int) {
	return c.originX, c.originY
}

// ExcludeWindow 自激防护：把指定窗口从抓屏中排除（PRD 4.3.7）
// windowID 为 Windows HWND。需 Windows 10 2004+；失败时返回错误由上层记录。
// 注意：GDI BitBlt 配合 CAPTUREBLT 会自然尊重 WDA_EXCLUDEFROMCAPTURE，
// 被排除窗口抓出来是透明/不可见，因此调用本方法即完成排除，无需 Capture 侧额外逻辑。
func (c *GDICapturer) ExcludeWindow(windowID int) error {
	if windowID == 0 {
		return nil
	}
	hwnd := uintptr(windowID)
	if err := procSetWindowDisplayAffinity.Find(); err != nil {
		return fmt.Errorf("当前系统不支持 SetWindowDisplayAffinity（需 Windows 10 2004+）: %w", err)
	}
	ret, _, _ := procSetWindowDisplayAffinity.Call(hwnd, uintptr(wdaExcludeFromCapture))
	if ret == 0 {
		return fmt.Errorf("SetWindowDisplayAffinity 失败 (hwnd=%d)，自激防护未生效", windowID)
	}
	return nil
}

// ExcludeSelfWindows 自激防护（PRD 4.3.7）：枚举当前进程的全部顶层窗口，
// 调用 SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE) 让它们对抓屏不可见。
// 这样主窗口 / 调试台 / 探针窗口都不会被自己的抓屏拍到，避免正反馈自激。
// 在 app 启动、窗口创建后调用一次即可（窗口销毁不影响已设置的亲和性）。
// 需 Windows 10 2004+；不支持时返回错误由上层记录（旧系统无自激防护，靠禁区兜底）。
func (c *GDICapturer) ExcludeSelfWindows() error {
	if err := procSetWindowDisplayAffinity.Find(); err != nil {
		return fmt.Errorf("当前系统不支持 SetWindowDisplayAffinity（需 Windows 10 2004+）: %w", err)
	}
	if err := procEnumWindows.Find(); err != nil {
		return fmt.Errorf("当前系统不支持 EnumWindows: %w", err)
	}
	pid := uint32(syscall.Getpid())
	var enumErr error
	cb := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
		var wpid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&wpid)))
		if wpid != pid {
			return 1 // 继续枚举
		}
		ret, _, _ := procSetWindowDisplayAffinity.Call(hwnd, uintptr(wdaExcludeFromCapture))
		if ret == 0 {
			enumErr = fmt.Errorf("SetWindowDisplayAffinity 失败 (hwnd=%d)，自激防护部分未生效", hwnd)
			// 不中断枚举，继续排除其他窗口
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return enumErr
}

// IsAuthorized Windows 没有 macOS 式的屏幕录制授权门禁，恒为可用。
// 真实限制来自锁屏 / UAC 安全桌面 / DRM 受保护内容，
// 表现为抓屏返回黑帧，由禁区检测与 IsBlackFrame 处理（PRD 第6章）。
func (c *GDICapturer) IsAuthorized() bool {
	return true
}

// IsBlackFrame 见 capture_common.go（双平台共享纯逻辑）。
// Windows 版直接复用：capture.IsBlackFrame。
