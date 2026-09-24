//go:build windows

package paths

import (
	"os"
	"path/filepath"
)

// BundlePath 返回 exe 所在目录（对应 macOS 的 .app 包路径）。
//
// Windows 没有 bundle 概念：安装器把 exe、models\、configs\ 放在同一目录，
// 因此"包路径"即安装目录。语义差异已在 ModelPath 中统一处理。
func BundlePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, e := filepath.EvalSymlinks(exe); e == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

// BundleResourcesDir 返回随程序分发的只读资源目录。
// Windows 安装形态下资源与 exe 同级（models\yolo26m.onnx、configs\policy.yaml）。
//
// 注意：若安装在 Program Files，该目录对普通用户不可写 ——
// 可写数据一律走 AppDir()（%APPDATA%\ScreenGuard），
// 否则会遇到"配置写不进去"且只在部分机器上复现的问题。
func BundleResourcesDir() string {
	return BundlePath()
}
