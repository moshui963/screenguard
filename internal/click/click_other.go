//go:build !darwin && !windows

package click

import (
	"fmt"

	"screenguard/internal/capture"
)

// StubClicker 未支持平台占位（darwin / windows 之外）
type StubClicker struct{}

// NewPlatformClicker 跨平台工厂（未支持平台的占位分支）
// 注意：原 NewCGEventClicker 在 darwin 侧带 cap 参数、在 !darwin 侧不带，
// 签名不一致会导致非 darwin 平台调用点编译失败，故统一收敛到本工厂。
func NewPlatformClicker(cap capture.Capturer) Clicker {
	return &StubClicker{}
}

func (s *StubClicker) Click(x, y float64, scaleFactor float64) error {
	return fmt.Errorf("点击器不可用（非 macOS）")
}

func (s *StubClicker) VerifyVanish(before interface{}) (bool, error) {
	return false, fmt.Errorf("不可用")
}

func (s *StubClicker) MoveTo(x, y float64, scaleFactor float64) {
	// 占位平台不支持移动光标，空实现满足接口
}

func (s *StubClicker) IsAuthorized() bool { return false }

func (s *StubClicker) RequestPermission() bool { return false }
