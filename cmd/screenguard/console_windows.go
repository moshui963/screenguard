//go:build windows

package main

import (
	"log"
	"os"
	"syscall"
)

var (
	modKernel32          = syscall.NewLazyDLL("kernel32.dll")
	procAllocConsole     = modKernel32.NewProc("AllocConsole")
	procGetConsoleWindow = modKernel32.NewProc("GetConsoleWindow")
	procGetStdHandle     = modKernel32.NewProc("GetStdHandle")
	// STD_OUTPUT_HANDLE / STD_ERROR_HANDLE 在 Windows 头里是 -11 / -12，
	// 用无符号全 1 减偏移表示，避免 const 阶段 "negative constant overflows uintptr"。
	constStdOutputHandle = ^uintptr(0) - 10 // -11
	constStdErrorHandle  = ^uintptr(0) - 11 // -12
	constInvalidHandle   = ^uintptr(0)      // INVALID_HANDLE_VALUE
)

// ensureConsole 让双击 exe 启动时弹出一个系统控制台窗口（实时显示日志），
// 与前端 GUI 并存 —— 即用户所说的"终端页面"。
// 已在终端内运行（开发期 / 控制台子系统）时 GetConsoleWindow 非空，不重复创建。
func ensureConsole() {
	// 已关联控制台窗口（如开发期在终端中运行）则直接返回，避免多开一个黑框
	if w, _, _ := procGetConsoleWindow.Call(); w != 0 {
		return
	}
	// GUI 子系统双击启动：分配一个新控制台窗口
	if r, _, _ := procAllocConsole.Call(); r == 0 {
		return // 分配失败则安静跳过，不影响 GUI 启动
	}
	// 把标准输出/错误与默认 logger 接到新控制台，使 log.Printf 实时可见
	if h, _, _ := procGetStdHandle.Call(constStdOutputHandle); h != 0 && h != constInvalidHandle {
		f := os.NewFile(h, "CONOUT$")
		os.Stdout = f
		os.Stderr = f
		log.SetOutput(f)
	}
}
