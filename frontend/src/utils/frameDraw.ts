// ============================================================
// 调试台画布绘制
//
// 三个画布展示同一帧的三种形态，这是排查坐标/尺度问题的核心工具：
//   原屏画布   = 屏幕真实画面 + 屏幕坐标框
//   模型输入   = 真正喂进张量的图（含 letterbox 黑边）+ 模型空间框
//   ROI 裁切   = 第二级实际吃进去的那一小块
//
// 坐标系约定（务必看清，混用会偏 2 倍或偏一个 pad）：
//   screen 像素  ← payload.scale →  JPEG 像素
//   screen 像素  ← payload.ratio/dw/dh → 模型像素（imgsz×imgsz）
//   box.box       是 screen 像素
//   box.box_model 是模型像素，后端已算好，前端不再换算
// ============================================================
import type { FramePayload, FrameBox } from '@/api'

export interface LayerFlags {
  container: boolean
  exit: boolean
  rejected: boolean
  click: boolean
  trace: boolean
}

// ---- 轨迹缓冲：记录最近若干帧的出口/点击中心点（screen 坐标）----
// 开启"轨迹"叠加层时把这些点连成淡出折线，直观看到目标移动路径（即跟即随）。
interface TrailPoint { x: number; y: number; color: string }
const trailPoints: TrailPoint[] = []
const MAX_TRAIL = 90

const COLORS = {
  container: '#fb923c', // 橙
  exitPass: '#4ade80', // 绿
  exitFail: '#f87171', // 红
  rejected: '#60a5fa', // 蓝
  clickCross: '#ffffff',
  modelBox: '#facc15',
  bg: '#000000',
  label: 'rgba(0,0,0,0.72)',
}

// ---- 位图缓存：同一帧可能被三个画布各画一次，避免重复解码 base64 ----
const bitmapCache = new Map<string, Promise<HTMLImageElement>>()

function loadImage(cacheKey: string, b64: string): Promise<HTMLImageElement> {
  const hit = bitmapCache.get(cacheKey)
  if (hit) return hit
  const p = new Promise<HTMLImageElement>((resolve, reject) => {
    const img = new Image()
    img.onload = () => resolve(img)
    img.onerror = () => reject(new Error('位图解码失败'))
    img.src = 'data:image/jpeg;base64,' + b64
  })
  bitmapCache.set(cacheKey, p)
  // 只保留最近几帧，防止内存涨
  if (bitmapCache.size > 6) {
    const first = bitmapCache.keys().next().value
    if (first) bitmapCache.delete(first)
  }
  return p
}

// ---- 画布尺寸与 DPR 处理（Retina 下不处理会糊）----
function prepareCanvas(canvas: HTMLCanvasElement, cssW: number, cssH: number): CanvasRenderingContext2D | null {
  const dpr = window.devicePixelRatio || 1
  const w = Math.max(1, Math.round(cssW * dpr))
  const h = Math.max(1, Math.round(cssH * dpr))
  if (canvas.width !== w || canvas.height !== h) {
    canvas.width = w
    canvas.height = h
  }
  const ctx = canvas.getContext('2d')
  if (!ctx) return null
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
  return ctx
}

/** 等比 contain 适配：返回图像在画布内的绘制矩形（CSS 像素） */
export function computeFit(cw: number, ch: number, iw: number, ih: number) {
  if (iw <= 0 || ih <= 0) return { dx: 0, dy: 0, dw: 0, dh: 0, k: 1 }
  const k = Math.min(cw / iw, ch / ih)
  const dw = iw * k
  const dh = ih * k
  return { dx: (cw - dw) / 2, dy: (ch - dh) / 2, dw, dh, k }
}

/** screen 像素 → JPEG 图像像素 */
export function screenToImage(p: FramePayload, x: number, y: number): [number, number] {
  return [x * p.scale, y * p.scale]
}

/** JPEG 图像像素 → screen 像素 */
export function imageToScreen(p: FramePayload, x: number, y: number): [number, number] {
  return [p.scale > 0 ? x / p.scale : x, p.scale > 0 ? y / p.scale : y]
}

/** screen 像素 → 模型输入像素（与后端 OriginalToModel 同一公式） */
export function screenToModel(p: FramePayload, x: number, y: number): [number, number] {
  return [x * p.ratio + p.dw, y * p.ratio + p.dh]
}

function boxColor(b: FrameBox): string {
  if (b.role === 'container') return COLORS.container
  if (!b.gate_passed) {
    return b.reject_reason ? COLORS.rejected : COLORS.exitFail
  }
  return COLORS.exitPass
}

function drawBoxLabel(ctx: CanvasRenderingContext2D, text: string, x: number, y: number, color: string) {
  ctx.font = '11px -apple-system, sans-serif'
  const w = ctx.measureText(text).width + 6
  const ty = y - 14 < 0 ? y + 2 : y - 14
  ctx.fillStyle = COLORS.label
  ctx.fillRect(x, ty, w, 14)
  ctx.fillStyle = color
  ctx.fillText(text, x + 3, ty + 10)
}

/** 画一帧的屏幕画面 + 叠加框。space='screen' 用 box，'model' 用 box_model */
function drawFrame(
  canvas: HTMLCanvasElement,
  p: FramePayload,
  layers: LayerFlags,
  space: 'screen' | 'model',
  hoverKey?: string,
) {
  const cssW = canvas.clientWidth
  const cssH = canvas.clientHeight
  if (cssW < 2 || cssH < 2) return
  const ctx = prepareCanvas(canvas, cssW, cssH)
  if (!ctx) return

  ctx.fillStyle = COLORS.bg
  ctx.fillRect(0, 0, cssW, cssH)

  // 屏幕位图两个画布共用同一条缓存，避免同一帧解码两次
  const key = `${p.ts}`
  loadImage(key, p.jpeg).then((img) => {
    // 异步回来后可能已经切到新帧，丢弃过期绘制
    if (bitmapCache.get(key) === undefined) return

    let fit
    if (space === 'screen') {
      fit = computeFit(cssW, cssH, p.img_w, p.img_h)
    } else {
      // 模型空间：整张 imgsz×imgsz 画布（含黑边）等比放进 CSS 区域
      fit = computeFit(cssW, cssH, p.imgsz, p.imgsz)
      ctx.fillStyle = '#000'
      ctx.fillRect(fit.dx, fit.dy, fit.dw, fit.dh)
    }

    if (space === 'screen') {
      ctx.drawImage(img, fit.dx, fit.dy, fit.dw, fit.dh)
    } else {
      // 把屏幕图按 ratio 缩放到模型画布内并加 pad
      const sw = p.screen_w * p.ratio
      const sh = p.screen_h * p.ratio
      ctx.drawImage(img, fit.dx + p.dw * fit.k, fit.dy + p.dh * fit.k, sw * fit.k, sh * fit.k)
    }

    const toCanvas = (x: number, y: number): [number, number] => {
      if (space === 'screen') {
        const [ix, iy] = screenToImage(p, x, y)
        return [fit.dx + ix * fit.k, fit.dy + iy * fit.k]
      }
      return [fit.dx + x * fit.k, fit.dy + y * fit.k]
    }

    // 轨迹：仅在 screen 空间记录一次（避免 model 画布重复记录）
    if (layers.trace) {
      if (space === 'screen') {
        for (const b of p.boxes || []) {
          if (b.role !== 'container' && b.gate_passed) {
            trailPoints.push({ x: b.center[0], y: b.center[1], color: boxColor(b) })
          }
        }
        if (trailPoints.length > MAX_TRAIL) trailPoints.splice(0, trailPoints.length - MAX_TRAIL)
      }
    } else {
      trailPoints.length = 0
    }

    for (const b of p.boxes || []) {
      if (b.role === 'container' && !layers.container) continue
      if (b.role !== 'container' && b.gate_passed && !layers.exit) continue
      if (b.role !== 'container' && !b.gate_passed && !layers.rejected) continue

      const rect = space === 'screen' ? b.box : b.box_model
      const [x1, y1] = toCanvas(rect[0], rect[1])
      const [x2, y2] = toCanvas(rect[2], rect[3])
      const w = x2 - x1
      const h = y2 - y1
      const color = space === 'model' ? COLORS.modelBox : boxColor(b)

      ctx.strokeStyle = color
      ctx.lineWidth = hoverKey && boxKey(b) === hoverKey ? 3 : 1.5
      ctx.strokeRect(x1, y1, w, h)

      const label = space === 'screen'
        ? `${b.cls_name} ${b.conf.toFixed(2)}${b.reject_reason ? ' ✗' + b.reject_reason : ''}`
        : `${Math.round(w)}×${Math.round(h)}px`
      drawBoxLabel(ctx, label, x1, y1, color)

      // 点击点：只画通过门控的出口
      if (layers.click && b.role !== 'container' && b.gate_passed) {
        const c = space === 'screen'
          ? toCanvas(...screenToImage(p, b.center[0], b.center[1]))
          : toCanvas((b.box_model[0] + b.box_model[2]) / 2, (b.box_model[1] + b.box_model[3]) / 2)
        ctx.strokeStyle = COLORS.clickCross
        ctx.lineWidth = 1
        ctx.beginPath()
        ctx.moveTo(c[0] - 6, c[1])
        ctx.lineTo(c[0] + 6, c[1])
        ctx.moveTo(c[0], c[1] - 6)
        ctx.lineTo(c[0], c[1] + 6)
        ctx.stroke()
      }
    }

    // 轨迹：把最近若干帧的出口/点击中心连成淡出折线（仅 screen 空间有坐标意义）
    if (space === 'screen' && layers.trace && trailPoints.length > 0) {
      let prev: [number, number] | null = null
      for (const tp of trailPoints) {
        const [cx, cy] = toCanvas(tp.x, tp.y)
        if (prev) {
          ctx.strokeStyle = tp.color
          ctx.globalAlpha = 0.22
          ctx.lineWidth = 2
          ctx.beginPath()
          ctx.moveTo(prev[0], prev[1])
          ctx.lineTo(cx, cy)
          ctx.stroke()
        }
        prev = [cx, cy]
      }
      ctx.globalAlpha = 1
      // 轨迹末端画一个鼠标箭头图标，标示"下一个点击位置"
      if (prev) {
        drawCursorIcon(ctx, prev[0], prev[1])
      }
    }
  }).catch(() => { /* 解码失败保持黑底 */ })
}

function boxKey(b: FrameBox): string {
  return `${b.cls_name}:${Math.round(b.box[0])},${Math.round(b.box[1])}`
}

// drawCursorIcon 在 (x,y) 处画一个经典鼠标指针箭头（白色填充+黑描边），
// tip 指向 (x,y) 本身，用于标示自动点击将发生的位置。
function drawCursorIcon(ctx: CanvasRenderingContext2D, x: number, y: number) {
  const pts: [number, number][] = [
    [0, 0], [0, 16], [4.2, 12.5], [7.2, 19], [9.6, 17.9], [6.6, 11.5], [11.8, 11.5],
  ]
  ctx.save()
  ctx.translate(x, y)
  ctx.scale(1.15, 1.15)
  ctx.beginPath()
  ctx.moveTo(pts[0][0], pts[0][1])
  for (let i = 1; i < pts.length; i++) ctx.lineTo(pts[i][0], pts[i][1])
  ctx.closePath()
  ctx.fillStyle = 'rgba(255, 255, 255, 0.95)'
  ctx.strokeStyle = 'rgba(0, 0, 0, 0.85)'
  ctx.lineWidth = 1.2
  ctx.lineJoin = 'round'
  ctx.shadowColor = 'rgba(0, 0, 0, 0.35)'
  ctx.shadowBlur = 3
  ctx.fill()
  ctx.shadowBlur = 0
  ctx.stroke()
  ctx.restore()
}

export function drawScreenFrame(canvas: HTMLCanvasElement, p: FramePayload, layers: LayerFlags, hoverKey?: string) {
  drawFrame(canvas, p, layers, 'screen', hoverKey)
}

export function drawModelFrame(canvas: HTMLCanvasElement, p: FramePayload, layers: LayerFlags) {
  drawFrame(canvas, p, layers, 'model')
}

/** ROI 画布：第二级实际输入的那一块 */
export function drawRoiFrame(canvas: HTMLCanvasElement, p: FramePayload) {
  const cssW = canvas.clientWidth
  const cssH = canvas.clientHeight
  if (cssW < 2 || cssH < 2) return
  const ctx = prepareCanvas(canvas, cssW, cssH)
  if (!ctx) return
  ctx.fillStyle = COLORS.bg
  ctx.fillRect(0, 0, cssW, cssH)
  if (!p.roi?.jpeg) {
    ctx.fillStyle = '#555'
    ctx.font = '12px sans-serif'
    ctx.fillText('无容器，未生成 ROI', 10, cssH / 2)
    return
  }
  const key = `${p.ts}:roi`
  loadImage(key, p.roi.jpeg).then((img) => {
    const fit = computeFit(cssW, cssH, img.width, img.height)
    ctx.drawImage(img, fit.dx, fit.dy, fit.dw, fit.dh)
    ctx.fillStyle = '#9cd'
    ctx.font = '11px sans-serif'
    ctx.fillText(`${p.roi!.w}×${p.roi!.h}px → imgsz2=${p.roi!.imgsz2}`, 6, cssH - 8)
  }).catch(() => {})
}

/** 命中测试：给定 CSS 坐标，返回其落在哪个框上（用于悬停高亮） */
export function hitTest(p: FramePayload, canvas: HTMLCanvasElement, cssX: number, cssY: number): FrameBox | null {
  const cssW = canvas.clientWidth
  const cssH = canvas.clientHeight
  const fit = computeFit(cssW, cssH, p.img_w, p.img_h)
  const sx = (cssX - fit.dx) / fit.k
  const sy = (cssY - fit.dy) / fit.k
  const [scrX, scrY] = imageToScreen(p, sx, sy)
  for (const b of p.boxes || []) {
    if (scrX >= b.box[0] && scrX <= b.box[2] && scrY >= b.box[1] && scrY <= b.box[3]) {
      return b
    }
  }
  return null
}
