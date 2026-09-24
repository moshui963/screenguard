package engine

import (
	"testing"

	"screenguard/internal/types"
)

// TestNoDetectionReturnsNil 验证：屏幕上什么都没检出时不产生事件（v0.26 回归：
// 空帧也返回 report_only，导致事件流被"发现「—」未点击（）"空噪声刷屏）
func TestNoDetectionReturnsNil(t *testing.T) {
	eng := NewEngine(buildObsCfg())
	eng.SetIdleFunc(func() float64 { return 999 })
	eng.SetForbiddenZone(false)

	ev := eng.Decide(nil, 1920, 1080, 10, 20)
	if ev != nil {
		t.Fatalf("空帧不应产生事件，实际 action=%q", ev.Action)
	}
}

// TestExtremeModeIgnoresUserActive 验证：极限模式下用户正在操作也立即点击（v0.27）
func TestExtremeModeIgnoresUserActive(t *testing.T) {
	cfg := buildObsCfg()
	cfg.Mode = types.ModeExtreme
	eng := NewEngine(cfg)
	eng.SetIdleFunc(func() float64 { return 0 }) // 模拟用户正在操作
	eng.SetForbiddenZone(false)

	eng.SetPolicy(&PolicySnapshot{
		Version:        "test-v",
		ConfThresholds: map[string]float64{"GuanBi": 0.1},
		ClassModes:     map[string]types.PolicyMode{"GuanBi": types.PolicyClick},
	})

	boxes := []types.DetectionBox{containerBox(), exitBox("GuanBi", 0.92)}
	ev := eng.Decide(boxes, 1920, 1080, 10, 20)
	if ev == nil {
		t.Fatal("Decide 返回 nil")
	}
	if ev.Action != types.ActionClick {
		t.Fatalf("极限模式应无视用户活跃立即点击，实际 %q", ev.Action)
	}
}

// TestContainerWithoutExitReportsNoExit 验证：只检出弹窗本体、未检出关闭按钮时，
// 产生带 no_exit 拒因的 report_only 事件（而非无解释的空上报）
func TestContainerWithoutExitReportsNoExit(t *testing.T) {
	eng := NewEngine(buildObsCfg())
	eng.SetIdleFunc(func() float64 { return 999 })
	eng.SetForbiddenZone(false)

	boxes := []types.DetectionBox{containerBox()}
	ev := eng.Decide(boxes, 1920, 1080, 10, 20)
	if ev == nil {
		t.Fatal("只检出容器时应产生 report_only 事件，实际返回 nil")
	}
	if ev.Action != types.ActionReportOnly {
		t.Fatalf("期望 ActionReportOnly，实际 %q", ev.Action)
	}
	if ev.PreemptedCause != "no_exit" {
		t.Fatalf("期望 PreemptedCause=no_exit，实际 %q", ev.PreemptedCause)
	}
}
