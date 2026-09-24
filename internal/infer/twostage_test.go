package infer

import (
	"image"
	"testing"

	"screenguard/internal/types"
)

// fakeDetector 不依赖 cgo / ONNX，用固定输出验证两级调度的契约。
type fakeDetector struct {
	level1 []types.DetectionBox
	level2 []types.DetectionBox
}

func (f *fakeDetector) DetectLevel1(img image.Image, imgsz int) ([]types.DetectionBox, int, error) {
	return f.level1, 1, nil
}
func (f *fakeDetector) DetectLevel2(roi image.Image, origin image.Point, imgsz2, w, h int) ([]types.DetectionBox, int, error) {
	// 模拟第二级：把出口坐标从 ROI 本地平移到屏幕坐标（真实实现里由 postprocess 做）
	out := make([]types.DetectionBox, 0, len(f.level2))
	for _, b := range f.level2 {
		nb := b
		nb.Box = types.BoxXYXY{b.Box[0] + float64(origin.X), b.Box[1] + float64(origin.Y),
			b.Box[2] + float64(origin.X), b.Box[3] + float64(origin.Y)}
		nb.Center = types.Center{b.Center[0] + float64(origin.X), b.Center[1] + float64(origin.Y)}
		out = append(out, nb)
	}
	return out, 1, nil
}
func (f *fakeDetector) Imgsz2(cw, ch int) int { return ComputeImgsz2(cw, ch) }
func (f *fakeDetector) ModelInputSize() int   { return 0 }

func container() types.DetectionBox {
	return types.DetectionBox{ClS: 0, ClSName: types.ContainerClass, Conf: 0.98,
		Box: types.BoxXYXY{700, 320, 1220, 700}, Center: types.Center{960, 510}}
}

func exitBox() types.DetectionBox {
	return types.DetectionBox{ClS: 1, ClSName: "GuanBi", Conf: 0.9,
		Box: types.BoxXYXY{484, 12, 506, 34}, Center: types.Center{495, 23}}
}

// 核心契约：一级容器必须带 RoleContainer，否则引擎筛不到容器，自动点击永不触发
func TestLevel1ContainersCarryRole(t *testing.T) {
	d := &fakeDetector{level1: []types.DetectionBox{container()}, level2: []types.DetectionBox{exitBox()}}
	res, err := TwoStageDetect(d, image.NewRGBA(image.Rect(0, 0, 1920, 1080)), 1920, 1080, 1280,
		map[string]float64{}, types.ClassNames)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Containers) != 1 {
		t.Fatalf("容器数 %d", len(res.Containers))
	}
	if res.Containers[0].Role != types.RoleContainer {
		t.Fatalf("一级容器 Role 应为 container，实际 %q —— 引擎将筛不到容器", res.Containers[0].Role)
	}
	for _, e := range res.Exits {
		if e.Role != types.RoleExit {
			t.Fatalf("出口 Role 应为 exit，实际 %q", e.Role)
		}
	}
}

// 第二级误检出的容器类不得混进 Exits（否则同一弹窗被算两次）
func TestLevel2ContainerNotLeakedIntoExits(t *testing.T) {
	d := &fakeDetector{
		level1: []types.DetectionBox{container()},
		level2: []types.DetectionBox{exitBox(), container()},
	}
	res, err := TwoStageDetect(d, image.NewRGBA(image.Rect(0, 0, 1920, 1080)), 1920, 1080, 1280,
		map[string]float64{}, types.ClassNames)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Exits {
		if e.ClSName == types.ContainerClass {
			t.Fatal("容器类泄漏进 Exits")
		}
	}
	if len(res.Exits) != 1 {
		t.Fatalf("出口数应为 1，得到 %d", len(res.Exits))
	}
}

// 出口坐标必须已还原到屏幕空间（否则点击会落在窗口内相对位置）
func TestExitCoordsInScreenSpace(t *testing.T) {
	d := &fakeDetector{level1: []types.DetectionBox{container()}, level2: []types.DetectionBox{exitBox()}}
	res, err := TwoStageDetect(d, image.NewRGBA(image.Rect(0, 0, 1920, 1080)), 1920, 1080, 1280,
		map[string]float64{}, types.ClassNames)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Exits) == 0 {
		t.Fatal("无出口")
	}
	// 容器 (700,320)-(1220,700)，外扩 15% → ROI 原点 (622,263)
	// fake 的本地出口中心 (495,23) → 屏幕应为 (1117, 286)
	c := res.Exits[0].Center
	if diff := c[0] - 1117; diff < -1 || diff > 1 {
		t.Fatalf("x 未正确还原: %v（期望 1117）", c[0])
	}
	if diff := c[1] - 286; diff < -1 || diff > 1 {
		t.Fatalf("y 未正确还原: %v（期望 286）", c[1])
	}
	// 且必须落在外扩后的 ROI 之内
	b := res.Exits[0].Box
	if b[0] < 622 || b[2] > 1298 || b[1] < 263 || b[3] > 757 {
		t.Fatalf("出口框越出 ROI: %v", b)
	}
}

// 无容器时不应调用第二级
func TestNoContainerSkipsLevel2(t *testing.T) {
	d := &fakeDetector{level1: nil, level2: []types.DetectionBox{exitBox()}}
	res, err := TwoStageDetect(d, image.NewRGBA(image.Rect(0, 0, 1920, 1080)), 1920, 1080, 1280,
		map[string]float64{}, types.ClassNames)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Exits) != 0 {
		t.Fatal("无容器时不应有出口")
	}
}

func TestImgsz2Clamped(t *testing.T) {
	if got := ComputeImgsz2(100, 100); got != 768 {
		t.Fatalf("小裁切应夹到下限 768，得到 %d", got)
	}
	if got := ComputeImgsz2(5000, 5000); got != 1280 {
		t.Fatalf("大裁切应夹到上限 1280，得到 %d", got)
	}
	if got := ComputeImgsz2(900, 600); got != 928 {
		t.Fatalf("900 应向上取 32 倍数=928，得到 %d", got)
	}
}

func TestImgszBaseline(t *testing.T) {
	cases := map[int]int{1920: 1440, 2560: 1920, 1366: 1024, 3840: 2048, 1024: 1024}
	for in, want := range cases {
		if got := ComputeImgszBaseline(in); got != want {
			t.Errorf("逻辑宽 %d: 期望 %d 得到 %d", in, want, got)
		}
	}
}
