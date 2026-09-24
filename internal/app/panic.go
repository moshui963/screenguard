package app

import (
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

// 崩溃诊断：Go 的 panic 会直接终结整个进程，而 GUI 子系统下 stderr 不可见，
// 导致"启动就崩退 / 用着用着崩退"无法定位。这里统一兜底：
//   - 捕获 panic，打印到控制台；
//   - 同时把完整堆栈追加到 crash.log（与 guard.db 同级），便于事后取证。
//
// 用法（在协程入口 or 单次循环体开头）：
//
//	defer app.RecoverPanic("inferLoop")
var (
	crashLogMu   sync.Mutex
	crashLogPath string
)

// SetCrashLogPath 设置崩溃日志落盘路径（New() 时调用）
func SetCrashLogPath(p string) {
	crashLogMu.Lock()
	crashLogPath = p
	crashLogMu.Unlock()
}

// CrashLogPath 返回当前崩溃日志路径（诊断/前端展示用）
func CrashLogPath() string {
	crashLogMu.Lock()
	defer crashLogMu.Unlock()
	return crashLogPath
}

// RecoverPanic 捕获 panic 并落盘堆栈；无 panic 时静默返回。
// 放在 defer 中调用，用于把"进程级崩溃"降级为"可读的诊断记录"。
func RecoverPanic(tag string) {
	r := recover()
	if r == nil {
		return
	}
	stack := debug.Stack()
	stamp := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf("\n===== PANIC @ %s | %s =====\npanic: %v\n\n%s\n", tag, stamp, r, stack)

	// 控制台（AllocConsole 后可见）
	log.Printf("[PANIC] %s: %v\n%s", tag, r, stack)

	// 落盘取证
	crashLogMu.Lock()
	path := crashLogPath
	crashLogMu.Unlock()
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(msg)
}
