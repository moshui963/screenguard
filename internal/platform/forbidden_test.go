package platform

import "testing"

func TestMatchForbiddenByProcessName_RemoteDesktop(t *testing.T) {
	var s ForbiddenZoneState
	MatchForbiddenByProcessName(&s, "com.microsoft.rdc.macos")
	if !s.RemoteDesktop {
		t.Fatal("应识别远程桌面客户端")
	}
	if s.SecureDesktop || s.Locked {
		t.Fatal("不应误判为其他禁区")
	}
}

func TestMatchForbiddenByProcessName_Installer(t *testing.T) {
	var s ForbiddenZoneState
	MatchForbiddenByProcessName(&s, "com.apple.installer")
	if !s.SecureDesktop {
		t.Fatal("应识别系统安装器为安全桌面等价")
	}
}

func TestMatchForbiddenByProcessName_ScreenSaver(t *testing.T) {
	var s ForbiddenZoneState
	MatchForbiddenByProcessName(&s, "com.apple.screensaver.engine")
	if !s.Locked {
		t.Fatal("应识别屏保为锁屏等价")
	}
}

func TestMatchForbiddenByProcessName_NormalApp(t *testing.T) {
	var s ForbiddenZoneState
	MatchForbiddenByProcessName(&s, "com.apple.Safari")
	if s.Any() {
		t.Fatalf("普通 App 不应命中任何禁区, got %+v", s)
	}
}

func TestForbiddenZoneState_Any_ExcludesOnBattery(t *testing.T) {
	// PRD 第6章：仅电池供电不是禁区，只有 LowBattery(<20%) 才是
	s := ForbiddenZoneState{OnBattery: true, BatteryPercent: 80}
	if s.Any() {
		t.Fatal("仅电池供电（80%）不应算禁区")
	}
	s2 := ForbiddenZoneState{LowBattery: true, BatteryPercent: 15}
	if !s2.Any() {
		t.Fatal("低电量(15%)应算禁区")
	}
}
