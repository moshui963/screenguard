package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"screenguard/internal/types"
)

// 日志目录：默认放"软件目录/log"（即 exe 同级 log 目录）。
//
// 之所以从 %APPDATA% 迁到软件目录：用户排查问题时最先翻的就是软件安装目录，
// 把运行日志 / 崩溃堆栈 / 事件流三样都集中在这里，不用再去 AppData 里找。
// 配置与数据库（config.json / policy.yaml / guard.db）属于"状态"，
// 仍留在用户目录，避免软件移到别处后配置丢失或需要重新授权。
//
// 可用 config.json 的 log_dir 覆盖；填相对路径时按 exe 所在目录解析。

// resolveLogDir 确定日志目录，并保证其存在。
func resolveLogDir(cfg *types.AppConfig, userDir string) string {
	exeDir := exeDirOrCWD()

	dir := strings.TrimSpace(cfg.LogDir)
	if dir == "" {
		dir = filepath.Join(exeDir, "log")
	} else if !filepath.IsAbs(dir) {
		dir = filepath.Join(exeDir, dir)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		// exe 目录不可写（如装在 Program Files 且无管理员权限）→ 回退用户目录
		fallback := filepath.Join(userDir, "log")
		log.Printf("[Paths] 日志目录 %s 不可用（%v），回退到 %s", dir, err, fallback)
		dir = fallback
		_ = os.MkdirAll(dir, 0o755)
	}
	return dir
}

// exeDirOrCWD 返回 exe 所在目录；取不到时退化为当前工作目录
func exeDirOrCWD() string {
	if exe, err := os.Executable(); err == nil {
		if p, err := filepath.EvalSymlinks(exe); err == nil {
			exe = p
		}
		return filepath.Dir(exe)
	}
	wd, _ := os.Getwd()
	return wd
}

// setupLogger 把标准 logger 同时输出到控制台与日志文件（screenguard-YYYYMMDD.log）。
// 返回打开的日志文件，供 main 在退出前关闭；失败时返回 nil（不影响启动）。
func setupLogger(dir string) *os.File {
	path := filepath.Join(dir, time.Now().Format("screenguard-20060102.log"))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("[Paths] 无法写日志文件 %s: %v", path, err)
		return nil
	}
	// 控制台（AllocConsole 之后 os.Stderr 就是那个黑框）+ 文件双写
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return f
}

// pruneOldLogs 清理超过保留天数的运行日志（默认 30 天，由 log_retention_days 控制）。
// 只删我们自己产出的 screenguard-YYYYMMDD.log，不碰 events/ 与 crash.log。
func pruneOldLogs(dir string, days int) {
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().AddDate(0, 0, -days)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var removed int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "screenguard-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			removed++
		}
	}
	if removed > 0 {
		log.Printf("[Paths] 已清理 %d 个超过 %d 天的历史日志", removed, days)
	}
}

// logPathsSummary 启动时把关键路径打全，避免"日志到底写哪了"的二次排查
func logPathsSummary(userDir, logDir, cfgPath, dbPath string) {
	fmt.Fprintf(os.Stderr, "\n")
	log.Printf("[Paths] 软件目录(exe): %s", exeDirOrCWD())
	log.Printf("[Paths] 日志目录:       %s", logDir)
	log.Printf("[Paths] 配置文件:       %s", cfgPath)
	log.Printf("[Paths] 事件数据库:     %s", dbPath)
}
