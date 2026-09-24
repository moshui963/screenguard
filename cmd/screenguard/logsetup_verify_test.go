package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"testing"

	"screenguard/internal/types"
)

// TestLogDirRouting 验证日志目录解析与双写落盘逻辑（无 GUI 依赖）。
func TestLogDirRouting(t *testing.T) {
	dir := t.TempDir()

	// 1) 显式 log_dir → 原样使用
	cfg := &types.AppConfig{LogDir: dir, LogRetentionDays: 1}
	resolved := resolveLogDir(cfg, t.TempDir())
	if resolved != dir {
		t.Fatalf("resolveLogDir(显式) 期望 %s，实际 %s", dir, resolved)
	}

	// 2) 空 log_dir → 退化为 exe 同级 log 目录
	cfg2 := &types.AppConfig{LogDir: "", LogRetentionDays: 1}
	resolved2 := resolveLogDir(cfg2, t.TempDir())
	if !filepath.IsAbs(resolved2) || filepath.Base(resolved2) != "log" {
		t.Fatalf("resolveLogDir(空) 应解析为 <exe>/log，实际 %s", resolved2)
	}

	// 3) setupLogger 必须真正把日志写到文件
	f := setupLogger(dir)
	if f == nil {
		t.Fatal("setupLogger 返回 nil，日志无法落盘")
	}
	log.Printf("[RoutingTest] 路由验证写入")
	// 先关闭以便读取落盘内容（MultiWriter 同时写 stderr，不影响文件刷新）
	f.Close()

	matches, _ := filepath.Glob(filepath.Join(dir, "screenguard-*.log"))
	if len(matches) == 0 {
		t.Fatal("未生成 screenguard-*.log")
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("读取日志失败: %v", err)
	}
	if !bytes.Contains(data, []byte("[RoutingTest] 路由验证写入")) {
		t.Fatalf("日志内容未落盘: %s", string(data))
	}
}
