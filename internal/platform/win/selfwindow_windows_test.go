//go:build windows

package win

import "testing"

// 越界点不应命中任何自身窗口：验证 EnumWindows 回调链路可正常执行且不 panic。
func TestIsPointInSelfWindowOffScreen(t *testing.T) {
	if IsPointInSelfWindow(-100000, -100000) {
		t.Fatal("屏幕外坐标不应落在任何自身窗口内")
	}
}
