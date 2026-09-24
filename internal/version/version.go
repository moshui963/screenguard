// Package version 提供编译期注入的版本号。
//
// 版本号规则（PRD/交付要求）：每次编译"本体"（screenguard.exe）时，
// 由构建脚本（scripts/build-win.ps1 / Makefile 的 win 目标）自动在 version.txt
// 上 +0.01（百分位自增，满 100 进位到主版本），并通过 -ldflags 注入本变量。
//
// 未走标准构建脚本时（如本地 go run），Version 保持 "dev"。
package version

// Version 由构建脚本通过 -ldflags "-X screenguard/internal/version.Version=x.yy" 注入。
var Version = "dev"
