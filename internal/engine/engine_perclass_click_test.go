package engine

import (
	"testing"

	"screenguard/internal/types"
)

// buildObsCfg 构造一个观察模式的引擎配置，便于单帧即可通过 4 帧一致性门控
func buildObsCfg() *types.AppConfig {
	cfg := types.DefaultConfig()
	cfg.Mode = types.ModeObserve
	cfg.FrameConsistencyN = 1
	cfg.FrameConsistencyStd = 100.0
	cfg.FrameConsistencyTTL = 8000
	cfg.SuppressRapidMs = 0
	cfg.ClickBudgetPerHour = 1000
	return cfg
}

func exitBox(cls string, conf float64) types.DetectionBox {
	return types.DetectionBox{
		ClSName:    cls,
		Conf:       conf,
		Box:        types.BoxXYXY{40, 40, 60, 60},
		Center:     types.Center{50, 50},
		Role:       types.RoleExit,
		GatePassed: true,
	}
}

func containerBox() types.DetectionBox {
	return types.DetectionBox{
		ClSName:    types.ContainerClass,
		Conf:       0.9,
		Box:        types.BoxXYXY{0, 0, 100, 100},
		Center:     types.Center{50, 50},
		Role:       types.RoleContainer,
		GatePassed: true,
	}
}

// TestPerClassClickInObserveMode 验证：观察模式下，逐类策略为 click 的类别确实会触发 ActionClick
func TestPerClassClickInObserveMode(t *testing.T) {
	eng := NewEngine(buildObsCfg())
	// 模拟"用户空闲"：返回很大的空闲秒数，绕过红线 P1-3 的活跃拦截
	eng.SetIdleFunc(func() float64 { return 999 })
	eng.SetForbiddenZone(false)

	snap := &PolicySnapshot{
		Version: "test-v",
		ConfThresholds: map[string]float64{
			"GuanBi": 0.1,
		},
		ClassModes: map[string]types.PolicyMode{
			"GuanBi": types.PolicyClick,
		},
	}
	eng.SetPolicy(snap)

	boxes := []types.DetectionBox{containerBox(), exitBox("GuanBi", 0.92)}
	ev := eng.Decide(boxes, 1920, 1080, 10, 20)
	if ev == nil {
		t.Fatal("Decide 返回 nil")
	}
	if ev.Action != types.ActionClick {
		t.Fatalf("期望 ActionClick（逐类 click 在观察模式应触发点击），实际 %q", ev.Action)
	}
}

// TestPerClassReportInObserveMode 验证：观察模式下，逐类策略为 report 的类别只上报不点击
func TestPerClassReportInObserveMode(t *testing.T) {
	eng := NewEngine(buildObsCfg())
	eng.SetIdleFunc(func() float64 { return 999 })
	eng.SetForbiddenZone(false)

	snap := &PolicySnapshot{
		Version: "test-v",
		ConfThresholds: map[string]float64{
			"GuanBi": 0.1,
		},
		ClassModes: map[string]types.PolicyMode{
			"GuanBi": types.PolicyReport,
		},
	}
	eng.SetPolicy(snap)

	boxes := []types.DetectionBox{containerBox(), exitBox("GuanBi", 0.92)}
	ev := eng.Decide(boxes, 1920, 1080, 10, 20)
	if ev == nil {
		t.Fatal("Decide 返回 nil")
	}
	if ev.Action != types.ActionReportOnly {
		t.Fatalf("期望 ActionReportOnly（逐类 report 在观察模式只上报），实际 %q", ev.Action)
	}
}

// TestPerClassDisabledInObserveMode 验证：逐类策略为 disabled 的类别被门控拦截
func TestPerClassDisabledInObserveMode(t *testing.T) {
	eng := NewEngine(buildObsCfg())
	eng.SetIdleFunc(func() float64 { return 999 })
	eng.SetForbiddenZone(false)

	snap := &PolicySnapshot{
		Version: "test-v",
		ConfThresholds: map[string]float64{
			"GuanBi": 0.1,
		},
		ClassModes: map[string]types.PolicyMode{
			"GuanBi": types.PolicyDisabled,
		},
	}
	eng.SetPolicy(snap)

	boxes := []types.DetectionBox{containerBox(), exitBox("GuanBi", 0.92)}
	ev := eng.Decide(boxes, 1920, 1080, 10, 20)
	if ev == nil {
		t.Fatal("Decide 返回 nil")
	}
	if ev.Action == types.ActionClick {
		t.Fatalf("disabled 类别不应触发点击，实际 %q", ev.Action)
	}
}
