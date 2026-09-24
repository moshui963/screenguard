package app

import (
	"encoding/base64"
	"image"
	"image/jpeg"
	"sync"
	"time"

	"screenguard/internal/infer"
	"screenguard/internal/types"
)

// ============================================================
// 帧推送 — 把"屏幕 → 模型输入 → 决策"三态喂给调试台画布
//
// 设计约束（PRD 附录A 工程约束 / 红线 P2）：
//  1. 推送必须限流。检测循环 2.5~3fps，但每帧编 JPEG + 走 IPC 是纯开销，
//     稳态下（屏幕无变化）没必要一直推。这里按 minInterval 节流，
//     并且"发生了决策"的帧强制推送（不能因为节流把关键帧丢掉）。
//  2. 图像必须降尺度。4K 一帧原始 RGBA 33MB，JPEG 后仍有数百 KB，
//     webview IPC 会被打满。统一压到 maxWidth 再编码。
//  3. 冻结帧（PRD 3.3.2）：暂停推送但保留最后一帧，让维护者能看清一闪而过的弹窗。
// ============================================================

// FrameBox 单个框的双坐标系表示
type FrameBox struct {
	ClS          int        `json:"cls"`
	ClSName      string     `json:"cls_name"`
	Conf         float64    `json:"conf"`
	Role         string     `json:"role"`
	Box          [4]float64 `json:"box"`       // 屏幕物理像素
	Center       [2]float64 `json:"center"`    // 屏幕物理像素
	BoxModel     [4]float64 `json:"box_model"` // 模型输入空间像素
	GatePassed   bool       `json:"gate_passed"`
	RejectReason string     `json:"reject_reason,omitempty"`
}

// FramePayload 一次推送的全部内容
type FramePayload struct {
	ScreenW int     `json:"screen_w"`
	ScreenH int     `json:"screen_h"`
	ImgW    int     `json:"img_w"` // JPEG 实际宽度（已降尺度）
	ImgH    int     `json:"img_h"`
	Scale   float64 `json:"scale"` // 屏幕像素 → JPEG 像素 的比例
	JpegB64 string  `json:"jpeg"`  // 不含 data: 前缀

	Imgsz int     `json:"imgsz"`
	Ratio float64 `json:"ratio"` // letterbox 缩放系数
	Dw    float64 `json:"dw"`
	Dh    float64 `json:"dh"`

	Boxes      []FrameBox `json:"boxes"`
	FrameIndex int64      `json:"frame_index"`
	CaptureMs  int        `json:"capture_ms"`
	ModelMs    int        `json:"model_ms"`
	TotalMs    int        `json:"total_ms"`

	Action string `json:"action,omitempty"`
	Result string `json:"result,omitempty"`

	// ROI：第一个容器的外扩裁切块（第二级实际输入来源），无容器时为空
	ROI      *ROIPayload `json:"roi,omitempty"`
	HasPopup bool        `json:"has_popup"`

	Frozen bool  `json:"frozen"`
	Ts     int64 `json:"ts"`
}

// ROIPayload 第二级裁切块信息
type ROIPayload struct {
	X       int    `json:"x"`
	Y       int    `json:"y"`
	W       int    `json:"w"`
	H       int    `json:"h"`
	Imgsz2  int    `json:"imgsz2"`
	JpegB64 string `json:"jpeg,omitempty"`
}

// jpegQ 画布推送用的默认编码参数（导出给测试复用）
var jpegQ = jpeg.Options{Quality: 62}

// defaultMaxWidth 推送图像的最大宽度
const defaultMaxWidth = 1280

// FramePusher 帧推送器
type FramePusher struct {
	mu sync.Mutex

	emit   func(name string, payload any) // Wails Event.Emit
	latest *FramePayload

	enabled     bool
	frozen      bool
	minInterval time.Duration
	lastPush    time.Time
	maxWidth    int
	quality     jpeg.Options

	pushed  int64
	skipped int64
}

// NewFramePusher 创建推送器
func NewFramePusher() *FramePusher {
	return &FramePusher{
		enabled:     true,
		minInterval: 450 * time.Millisecond, // ≈2.2fps
		maxWidth:    defaultMaxWidth,
		quality:     jpegQ,
	}
}

// SetEmitter 注入事件发送函数（Wails 启动后调用）
func (p *FramePusher) SetEmitter(f func(name string, payload any)) {
	p.mu.Lock()
	p.emit = f
	p.mu.Unlock()
}

// SetEnabled 开关推送（关闭时省掉编码开销）
func (p *FramePusher) SetEnabled(v bool) {
	p.mu.Lock()
	p.enabled = v
	p.mu.Unlock()
}

// SetFrozen 冻结：停止推送，前端保留最后一帧
func (p *FramePusher) SetFrozen(v bool) {
	p.mu.Lock()
	p.frozen = v
	p.mu.Unlock()
}

// Frozen 当前是否冻结
func (p *FramePusher) Frozen() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.frozen
}

// Latest 返回最近一帧（前端初次加载时拉一次，避免等下一个事件）
func (p *FramePusher) Latest() *FramePayload {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.latest
}

// Stats 推送统计（诊断"画面不动"是节流还是没推）
func (p *FramePusher) Stats() (pushed, skipped int64, enabled, frozen bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pushed, p.skipped, p.enabled, p.frozen
}

// interesting 判定这帧是否值得强制推送（绕过节流）
// 有容器 / 有出口 / 发生了动作 —— 这些是维护者要看的
func interesting(boxes []types.DetectionBox, action types.Action) bool {
	if len(boxes) > 0 {
		return true
	}
	switch action {
	case types.ActionClick, types.ActionReportOnly:
		return true
	}
	return false
}

// Push 编码并推送一帧。非阻塞：emit 失败只记日志不影响检测循环。
func (p *FramePusher) Push(img image.Image, lb infer.LetterboxParams, imgsz int,
	containers, exits []types.DetectionBox, event *types.DetectionEvent,
	captureMs, modelMs int, screenW, screenH int) {

	p.mu.Lock()
	if !p.enabled || p.frozen {
		p.mu.Unlock()
		return
	}
	now := time.Now()
	force := event != nil && interesting(append(append([]types.DetectionBox{}, containers...), exits...), event.Action)
	if !force && now.Sub(p.lastPush) < p.minInterval {
		p.skipped++
		p.mu.Unlock()
		return
	}
	p.lastPush = now
	p.pushed++
	emit := p.emit
	p.mu.Unlock()

	payload := BuildFramePayload(img, lb, imgsz, containers, exits, event,
		captureMs, modelMs, screenW, screenH, p.maxWidth, p.quality)
	if payload == nil {
		return
	}
	payload.Frozen = p.Frozen()

	p.mu.Lock()
	p.latest = payload
	emitFn := p.emit
	p.mu.Unlock()

	if emitFn != nil {
		emitFn("sg:frame", payload)
	} else if emit != nil {
		emit("sg:frame", payload)
	}
}

// DecodeB64ToBytes 解码 FramePayload 里的 base64 图像（诊断工具落盘用）
func DecodeB64ToBytes(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// NewFramePayload 用默认编码参数（maxWidth 1280 / quality 62）构造 payload。
// 线上推送与离线诊断都应走这个入口，保证两套口径完全一致。
func NewFramePayload(img image.Image, lb infer.LetterboxParams, imgsz int,
	containers, exits []types.DetectionBox, event *types.DetectionEvent,
	captureMs, modelMs, screenW, screenH int) *FramePayload {
	return BuildFramePayload(img, lb, imgsz, containers, exits, event,
		captureMs, modelMs, screenW, screenH, defaultMaxWidth, jpegQ)
}

// BuildFramePayload 纯函数：图像 + 检测结果 → FramePayload。
// 导出供 cmd/frameprobe 等离线诊断工具复用，确保线上与诊断走同一套换算。
func BuildFramePayload(img image.Image, lb infer.LetterboxParams, imgsz int,
	containers, exits []types.DetectionBox, event *types.DetectionEvent,
	captureMs, modelMs, screenW, screenH, maxWidth int, q jpeg.Options) *FramePayload {

	if img == nil {
		return nil
	}

	scaled, sw, sh, scale := downscale(img, maxWidth)
	jpgBytes, err := encodeJPEG(scaled, q.Quality)
	if err != nil {
		return nil
	}

	fp := &FramePayload{
		ScreenW:   screenW,
		ScreenH:   screenH,
		ImgW:      sw,
		ImgH:      sh,
		Scale:     scale,
		JpegB64:   b64(jpgBytes),
		Imgsz:     imgsz,
		Ratio:     lb.Ratio[0],
		Dw:        lb.Dw,
		Dh:        lb.Dh,
		CaptureMs: captureMs,
		ModelMs:   modelMs,
		TotalMs:   captureMs + modelMs,
		HasPopup:  len(containers) > 0,
		Ts:        time.Now().UnixMilli(),
	}

	add := func(b types.DetectionBox) {
		mb := lb.OriginalToModel(b.Box[0], b.Box[1], b.Box[2], b.Box[3])
		fp.Boxes = append(fp.Boxes, FrameBox{
			ClS: b.ClS, ClSName: b.ClSName, Conf: b.Conf, Role: string(b.Role),
			Box:          [4]float64{b.Box[0], b.Box[1], b.Box[2], b.Box[3]},
			Center:       [2]float64{b.Center[0], b.Center[1]},
			BoxModel:     [4]float64{mb[0], mb[1], mb[2], mb[3]},
			GatePassed:   b.GatePassed,
			RejectReason: b.RejectReason,
		})
	}
	for _, b := range containers {
		add(b)
	}
	for _, b := range exits {
		add(b)
	}

	if event != nil {
		fp.Action = string(event.Action)
		fp.Result = string(event.Result)
		fp.FrameIndex = event.FrameIndex
	}

	// ROI：第一个容器外扩 15%（与两级调度同一口径），裁出来单独编码
	if len(containers) > 0 {
		c := containers[0].Box
		padX := (c[2] - c[0]) * 0.15
		padY := (c[3] - c[1]) * 0.15
		x0 := clampI(int(c[0]-padX), 0, screenW-1)
		y0 := clampI(int(c[1]-padY), 0, screenH-1)
		x1 := clampI(int(c[2]+padX), x0+1, screenW)
		y1 := clampI(int(c[3]+padY), y0+1, screenH)
		if sub := crop(img, x0, y0, x1, y1); sub != nil {
			subScaled, _, _, _ := downscale(sub, 640)
			if jb, e := encodeJPEG(subScaled, q.Quality); e == nil {
				mx := x1 - x0
				if y1-y0 > mx {
					mx = y1 - y0
				}
				fp.ROI = &ROIPayload{
					X: x0, Y: y0, W: x1 - x0, H: y1 - y0,
					Imgsz2:  infer.ComputeImgsz2(mx, mx),
					JpegB64: b64(jb),
				}
			}
		}
	}

	return fp
}
