//go:build !windows

package main

// 非 Windows 平台无需分配控制台（开发期本就在终端中运行），留空实现以保持跨平台编译。
func ensureConsole() {}
