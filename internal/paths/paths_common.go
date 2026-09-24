// Package paths 统一管理应用的运行时路径。
//
// 为什么必须集中管理：
// 桌面 app 会被移到任意目录、拷给同事、在只读位置运行、或从任意 CWD 启动。
// 任何"相对当前目录"的路径都会在这些场景下失败，最坏的情况是
// 把配置文件和数据写进别人不想被写的目录。
//
// 平台分工（原单文件实现里混着 macOS .app 结构假设，
// 导致 Windows 下 BundleResourcesDir() 恒为空、模型查找失败，故拆开）：
//
//	paths_common.go  —— 与平台无关的通用逻辑（数据目录、模型路径解析）
//	paths_darwin.go  —— macOS: ~/Library/Application Support + .app/Contents/Resources
//	paths_windows.go —— Windows: %APPDATA% + exe 同目录只读资源
package paths

import (
	"os"
	"path/filepath"
)

const appName = "ScreenGuard"

// AppDir 返回用户级数据目录，必要时创建。
//
//	darwin:  ~/Library/Application Support/ScreenGuard
//	windows: %APPDATA%\ScreenGuard
//
// 两者语义一致：每用户、可写、且不随程序安装位置变化。
// 可写数据绝不放安装目录内（Program Files 需要提权、.app 可能被 Gatekeeper 锁定）。
func AppDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, appName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// ModelPath 解析 ONNX 模型路径，按优先级：
//  1. 配置里显式指定的绝对路径
//  2. 随程序分发的只读资源目录 <resources>/models/<name>
//  3. 用户数据目录下 models/<name>（开发 / 热替换形态）
//  4. 相对当前工作目录兜底
//  5. 相对 exe 所在目录兜底（开发形态：模型放在项目内）
//
// 返回找到的绝对路径；全部失败时返回 cfgPath 原值（让上层报出可读错误）。
func ModelPath(cfgPath string) string {
	name := filepath.Base(cfgPath)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "model.onnx"
	}

	if filepath.IsAbs(cfgPath) {
		if fileExists(cfgPath) {
			return cfgPath
		}
	}

	if res := BundleResourcesDir(); res != "" {
		p := filepath.Join(res, "models", name)
		if fileExists(p) {
			return p
		}
	}

	if dir, err := AppDir(); err == nil {
		p := filepath.Join(dir, "models", name)
		if fileExists(p) {
			return p
		}
	}

	if p, err := filepath.Abs(cfgPath); err == nil && fileExists(p) {
		return p
	}

	// 相对 exe 所在目录（开发形态兜底）
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), cfgPath)
		if fileExists(p) {
			return p
		}
	}

	return cfgPath
}

// EnsureModelInUserDir 把只读资源区的模型复制到用户目录（可选，供后续热替换用）
func EnsureModelInUserDir(modelPath string) (string, error) {
	dir, err := AppDir()
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "models", filepath.Base(modelPath))
	if fileExists(dst) {
		return dst, nil
	}
	data, err := os.ReadFile(modelPath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", err
	}
	return dst, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
