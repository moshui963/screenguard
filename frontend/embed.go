package frontend

import "embed"

// Dist 把前端构建产物嵌入二进制，使 .app 位置无关。
//
// 为什么必须嵌入而不是运行时读磁盘：
// 桌面应用会被移动到 /Applications、被拷贝给同事、或在只读卷上运行，
// 任何依赖 "可执行文件同级/上级目录的 frontend/dist" 的做法都会在这些场景下
// 找不到资源，表现为 Wails 的 "Missing index.html" 错误页。
//
// 构建顺序：Makefile 先 `npm run build` 产出 frontend/dist，再 go build。
// 若 dist 为空目录，frontend/dist/.keep 保证 embed 指令合法。
//
//go:embed all:dist
var Dist embed.FS
