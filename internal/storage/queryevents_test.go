package storage

import (
	"path/filepath"
	"testing"
	"time"

	"screenguard/internal/types"
)

// TestQueryEventsEnriched 验证事件流查询返回"人类可读"所需的全部字段：
// 类别名、置信度、屏幕位置/尺寸、是否过闸、拒因、容器框、真实落点。
// 这是"事件流一堆英文看不懂"的根因回归测试 —— 旧实现只返回 box_count，
// 前端拿不到 boxes，类别与位置列永远是 "—"。
func TestQueryEventsEnriched(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	ev := &types.DetectionEvent{
		Ts:            time.Now(),
		SessionID:     "s1",
		PolicyVersion: "v1-default",
		FrameIndex:    7,
		ScreenW:       2560,
		ScreenH:       1440,
		CaptureMs:     11,
		ModelMs:       23,
		TotalMs:       34,
		Action:        types.ActionClick,
		Result:        types.ResultVanished,
		Boxes: []types.DetectionBox{
			{ClS: 0, ClSName: "TanChuang", Conf: 0.91,
				Box:    [4]float64{800, 400, 1600, 900},
				Center: [2]float64{1200, 650}, Role: types.RoleContainer, GatePassed: true},
			{ClS: 1, ClSName: "GuanBi", Conf: 0.87,
				Box:    [4]float64{1500, 420, 1560, 460},
				Center: [2]float64{1530, 440}, Role: types.RoleExit, GatePassed: true},
			{ClS: 5, ClSName: "TuiChu", Conf: 0.20,
				Box:    [4]float64{100, 100, 200, 160},
				Center: [2]float64{150, 130}, Role: types.RoleExit,
				GatePassed: false, RejectReason: "low_conf"},
		},
	}
	id, err := db.InsertEvent(ev)
	if err != nil {
		t.Fatalf("插入事件失败: %v", err)
	}
	if err := db.InsertClickAction(&types.ClickAction{
		EventID: id, PointPx: [2]float64{1530, 440}, PointPt: [2]float64{765, 220},
		ScaleFactor: 2, ExecutedAt: time.Now(), VerifyResult: types.ResultVanished,
		RetryN: 0, LatencyMs: 12,
	}); err != nil {
		t.Fatalf("插入点击记录失败: %v", err)
	}

	rows, err := db.QueryEvents(10, 0, "")
	if err != nil {
		t.Fatalf("查询事件失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("期望 1 条事件，实际 %d 条", len(rows))
	}
	r := rows[0]

	// 主目标应是"已过闸的出口"GuanBi，而不是容器或低置信度的 TuiChu
	if got := r["cls_name"]; got != "GuanBi" {
		t.Errorf("主目标类别错误: 期望 GuanBi, 实际 %v", got)
	}
	if got := r["conf"]; got != 0.87 {
		t.Errorf("置信度错误: 期望 0.87, 实际 %v", got)
	}
	if got := r["cx"]; got != 1530.0 {
		t.Errorf("中心 X 错误: 期望 1530, 实际 %v", got)
	}
	if got := r["cy"]; got != 440.0 {
		t.Errorf("中心 Y 错误: 期望 440, 实际 %v", got)
	}
	if got := r["w"]; got != 60.0 {
		t.Errorf("框宽错误: 期望 60, 实际 %v", got)
	}
	if got := r["role"]; got != "exit" {
		t.Errorf("角色错误: 期望 exit, 实际 %v", got)
	}
	if got := r["gate_passed"]; got != true {
		t.Errorf("过闸标记错误: 期望 true, 实际 %v", got)
	}
	// 容器框（弹窗本体）
	if got := r["container_cls"]; got != "TanChuang" {
		t.Errorf("容器类别错误: 期望 TanChuang, 实际 %v", got)
	}
	if got := r["container_x1"]; got != 800.0 {
		t.Errorf("容器 x1 错误: 期望 800, 实际 %v", got)
	}
	// 真实落点
	if got := r["click_x"]; got != 1530.0 {
		t.Errorf("落点 X 错误: 期望 1530, 实际 %v", got)
	}
	if got := r["click_verify"]; got != "vanished" {
		t.Errorf("复核结果错误: 期望 vanished, 实际 %v", got)
	}
	// 事件本体
	if got := r["action"]; got != "click" {
		t.Errorf("动作错误: 期望 click, 实际 %v", got)
	}
	if got := r["screen_w"]; got != int64(2560) {
		t.Errorf("屏幕宽错误: 期望 2560, 实际 %v", got)
	}
	if got := r["box_count"]; got != int64(3) {
		t.Errorf("框数量错误: 期望 3, 实际 %v", got)
	}
}

// TestQueryEventsFilterByClassName 验证按类别名过滤（前端下拉框传的是名字）
func TestQueryEventsFilterByClassName(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "test2.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	if _, err := db.InsertEvent(&types.DetectionEvent{
		Ts: time.Now(), SessionID: "s", PolicyVersion: "v1", Action: types.ActionClick,
		Boxes: []types.DetectionBox{{ClS: 1, ClSName: "GuanBi", Conf: 0.9, Role: types.RoleExit, GatePassed: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertEvent(&types.DetectionEvent{
		Ts: time.Now(), SessionID: "s", PolicyVersion: "v1", Action: types.ActionReportOnly,
		Boxes: []types.DetectionBox{{ClS: 8, ClSName: "ZhiDaoLe", Conf: 0.8, Role: types.RoleExit, GatePassed: true}},
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := db.QueryEvents(10, 0, "cls:ZhiDaoLe")
	if err != nil {
		t.Fatalf("按类别名过滤失败: %v", err)
	}
	if len(rows) != 1 || rows[0]["cls_name"] != "ZhiDaoLe" {
		t.Fatalf("按类别名过滤结果错误: %+v", rows)
	}

	rows, err = db.QueryEvents(10, 0, "action:click")
	if err != nil {
		t.Fatalf("按动作过滤失败: %v", err)
	}
	if len(rows) != 1 || rows[0]["cls_name"] != "GuanBi" {
		t.Fatalf("按动作过滤结果错误: %+v", rows)
	}
}
