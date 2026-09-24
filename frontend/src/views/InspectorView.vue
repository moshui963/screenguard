<template>
  <div class="inspector">
    <!-- ==================== 左栏：模型 / 类别控制 / 运行控制 ==================== -->
    <aside class="left-panel">
      <!-- 模型与阈值 -->
      <div class="lp-card">
        <div class="lp-title">模型与阈值</div>
        <div class="lp-row">
          <span class="lp-label">当前模型</span>
          <span class="lp-value ellipsis">{{ modelInfo.name || '—' }}</span>
        </div>
        <div class="lp-row">
          <span class="lp-label">格式</span>
          <span class="lp-value">{{ modelInfo.format || 'onnx' }}</span>
        </div>
        <div class="lp-row">
          <span class="lp-label">imgsz</span>
          <span class="lp-value">{{ modelInfo.imgsz || '—' }}</span>
        </div>
        <div class="lp-row">
          <span class="lp-label">推理线程</span>
          <span class="lp-value">{{ modelInfo.threads || '—' }}</span>
        </div>
        <div class="lp-row">
          <span class="lp-label">单帧耗时</span>
          <span class="lp-value" :class="{ yellow: (modelInfo.lastMs || 0) > 800 }">
            {{ modelInfo.lastMs ? modelInfo.lastMs + 'ms' : '—' }}
          </span>
        </div>
        <div class="lp-row">
          <span class="lp-label">帧序号</span>
          <span class="lp-value">{{ frame?.frame_index ?? '—' }}</span>
        </div>
        <div class="lp-row">
          <span class="lp-label">推送/跳过</span>
          <span class="lp-value">{{ frameStats.pushed ?? 0 }} / {{ frameStats.skipped ?? 0 }}</span>
        </div>
        <div class="lp-row">
          <span class="lp-label">决策</span>
          <span class="lp-value" :class="actionColor(frame?.action || '')">{{ frame?.action || '无' }}</span>
        </div>
        <button class="lp-btn full" @click="loadModelVersions">刷新模型信息</button>
      </div>

      <!-- 类别控制：单一控制面 = 名称 + 阈值 + 模式 + 应用（移除右下角重复的应用按钮） -->
      <div class="lp-card">
        <div class="lp-title">类别控制</div>
        <div class="cls-quick" v-for="c in classControls" :key="c.name">
          <span class="cls-quick-name" :class="modeDotClass(c.mode)">●</span>
          <span class="cls-quick-label">{{ c.name }}</span>
          <input type="number" min="0.05" max="0.95" step="0.05"
            v-model.number="c.conf" class="conf-input-sm" @change="classModeChanged = true" />
          <select v-model="c.mode" class="mode-select" @change="classModeChanged = true">
            <option value="click">自动点</option>
            <option value="report">只上报</option>
            <option value="disabled">禁用</option>
          </select>
        </div>
        <div class="lp-hint text-sm text-dim" v-if="classModeChanged">
          有未应用的修改
          <button class="lp-btn primary" @click="applyPolicy">应用</button>
        </div>
      </div>

      <!-- 运行控制 -->
      <div class="lp-card">
        <div class="lp-title">运行控制</div>
        <div class="lp-btn-group">
          <button class="lp-btn" :class="{ active: !frozen }" @click="frozen && toggleFreeze()">▶ 运行</button>
          <button class="lp-btn" :class="{ active: frozen }" @click="!frozen && toggleFreeze()">⏸ 冻结帧</button>
        </div>
        <div class="lp-btn-group">
          <button class="lp-btn" @click="loadEvents">⟳ 刷新事件流</button>
          <button class="lp-btn" @click="refreshStats">⟳ 帧统计</button>
        </div>
        <div class="lp-hint text-sm text-dim">
          冻结时暂停采集但保留当前帧与全部标注
        </div>
      </div>
    </aside>

    <!-- ==================== 中区：双画布 + 事件流（主角） ==================== -->
    <div class="center-panel">
      <!-- 双画布对照 -->
      <div class="canvas-row">
        <!-- 左画布：原屏画面 -->
        <div class="canvas-panel">
          <div class="panel-header">
            <span class="ph-title">原屏画面</span>
            <div class="layer-toggles">
              <label><input type="checkbox" v-model="app.layers.container" /> 容器</label>
              <label><input type="checkbox" v-model="app.layers.exit" /> 出口</label>
              <label><input type="checkbox" v-model="app.layers.rejected" /> 被拒</label>
              <label><input type="checkbox" v-model="app.layers.click" /> 点击点</label>
              <label><input type="checkbox" v-model="app.layers.trace" /> 轨迹</label>
            </div>
          </div>
          <div class="canvas-wrap">
            <canvas ref="leftCanvas" class="inspector-canvas"
              @mousemove="onLeftMove" @mouseleave="onLeftLeave"></canvas>
            <div v-if="!frame" class="canvas-empty text-sm">
              等待帧推送…（需授予屏幕录制权限并启动检测）
            </div>
            <div v-if="hoverInfo" class="hover-tip">{{ hoverInfo }}</div>
          </div>
        </div>

        <!-- 右画布：模型输入 letterbox 后 -->
        <div class="canvas-panel">
          <div class="panel-header">
            <span class="ph-title">模型输入</span>
            <span class="text-dim text-sm" v-if="modelInputInfo">{{ modelInputInfo }}</span>
          </div>
          <div class="canvas-wrap">
            <canvas ref="rightCanvas" class="inspector-canvas"></canvas>
          </div>
        </div>

        <!-- ROI 裁切预览：改为窄条，按需 -->
        <div class="roi-panel">
          <div class="panel-header"><span class="ph-title">ROI 裁切</span></div>
          <div class="roi-preview">
            <canvas ref="roiCanvas"></canvas>
          </div>
          <div class="roi-info text-sm" v-if="roiInfo">{{ roiInfo }}</div>
        </div>
      </div>

      <!-- 事件流（主角，占满剩余空间） -->
      <div class="event-stream card">
        <div class="panel-header">
          <span class="ph-title">事件流</span>
          <div class="event-filters">
            <select v-model="filter.action" class="filter-select">
              <option value="">全部</option>
              <option value="click">真的点了</option>
              <option value="report_only">只记录</option>
              <option value="blocked_user_active">用户操作中</option>
              <option value="low_conf">低置信度</option>
              <option value="suppressed_rapid">重复抑制</option>
            </select>
            <select v-model="filter.cls" class="filter-select">
              <option value="">所有类别</option>
              <option v-for="c in classNames" :key="c" :value="c">{{ c }}</option>
            </select>
          </div>
          <span class="text-sm text-dim">{{ filteredEvents.length }} 条</span>
        </div>
        <div class="event-list" ref="eventListRef">
          <div v-if="filteredEvents.length === 0" class="event-empty text-sm text-dim">
            暂无事件。检测到候选目标后会出现在这里；被拒绝的也会（带原因）。
          </div>
          <div v-for="e in filteredEvents" :key="e.id" class="event-row"
            :class="eventColor(e)" @click="selectEvent(e)">
            <div class="ev-line1">
              <span class="ev-time">{{ formatTime(e.ts) }}</span>
              <span class="ev-head" :class="actionColor(e.action)">{{ eventHeadline(e) }}</span>
              <span class="ev-verdict" :class="verdictColor(e)">{{ verdictCN(e) }}</span>
              <span class="ev-ms">{{ e.total_ms }}ms</span>
            </div>
            <div class="ev-line2">{{ eventDetail(e) }}</div>
          </div>
        </div>
      </div>
    </div>

    <!-- ==================== 右栏：详情抽屉（按需展开） ==================== -->
    <aside class="detail-drawer card" v-if="selectedEvent">
      <div class="panel-header">
        <span class="ph-title">事件详情 #{{ selectedEvent.id }}</span>
        <button class="text-btn" @click="selectedEvent = null">✕</button>
      </div>
      <div class="detail-content scroll-y">
        <div class="detail-section">
          <div class="detail-label">时间</div>
          <div>{{ formatTime(selectedEvent.ts) }}</div>
        </div>
        <div class="detail-section">
          <div class="detail-label">策略版本</div>
          <div>{{ selectedEvent.policy_version }}</div>
        </div>
        <div class="detail-section">
          <div class="detail-label">屏幕尺寸</div>
          <div>{{ selectedEvent.screen_w }}×{{ selectedEvent.screen_h }}</div>
        </div>
        <div class="detail-section">
          <div class="detail-label">耗时</div>
          <div>推理 {{ selectedEvent.model_ms }}ms / 总 {{ selectedEvent.total_ms }}ms</div>
        </div>
        <div class="detail-section">
          <div class="detail-label">事件摘要</div>
          <div>{{ eventHeadline(selectedEvent) }}</div>
          <div class="text-sm text-dim">{{ eventDetail(selectedEvent) }}</div>
        </div>
        <div class="detail-section">
          <div class="detail-label">帧与耗时</div>
          <div>帧#{{ selectedEvent.frame_index }} · 截屏 {{ selectedEvent.capture_ms }}ms · 推理 {{ selectedEvent.model_ms }}ms · 总 {{ selectedEvent.total_ms }}ms</div>
        </div>
        <div class="detail-section">
          <div class="detail-label">原始数据（含被拒框与拒因）</div>
          <pre class="box-list">{{ JSON.stringify(selectedEvent, null, 2) }}</pre>
        </div>
      </div>
    </aside>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useAppStore } from '@/stores/app'
import { api, onWailsFrame, hasWailsEvents, type FramePayload, type FrameBox } from '@/api'
import {
  drawScreenFrame, drawModelFrame, drawRoiFrame, hitTest, screenToModel,
} from '@/utils/frameDraw'

const app = useAppStore()

// ============ 左栏 ============
const modelInfo = ref<any>({ name: 'yolo26m', format: 'onnx', imgsz: 0, threads: 0, lastMs: 0 })
const classModeChanged = ref(false)

// ============ 画布 ============
const leftCanvas = ref<HTMLCanvasElement>()
const rightCanvas = ref<HTMLCanvasElement>()
const roiCanvas = ref<HTMLCanvasElement>()
const frozen = ref(false)
const hoverInfo = ref('')
const modelInputInfo = ref('')
const roiInfo = ref('')
const frame = ref<FramePayload | null>(null)
const frameStats = ref<any>({ pushed: 0, skipped: 0, enabled: true, frozen: false })
const hoverKey = ref<string | undefined>(undefined)

// 叠加层开关提升到 store（app.layers），跨页面导航保持勾选状态，即跟即随

// ============ 类别控制 ============
const classNames = ['TanChuang', 'GuanBi', 'GuanBi_str', 'XiaYiBu', 'QuXiao',
  'TuiChu', 'TiaoGuo', 'WoBuShou', 'ZhiDaoLe', 'YunXu']
const classControls = ref<{ name: string; conf: number; mode: string; clicks: number; failures: number }[]>([])

// 从后端加载真实逐类策略（conf + mode），保证 UI 与引擎快照一致，
// 并在保存后回显，避免"选了点击又被回退/无反应"。
async function loadCategoryPolicy() {
  try {
    const pol = await api.getCategoryPolicy()
    classControls.value = classNames.map((name) => {
      const cp = pol[name]
      return {
        name,
        conf: cp?.conf ?? 0.4,
        mode: cp?.mode ?? (name === 'TanChuang' ? 'disabled' : 'click'),
        clicks: 0,
        failures: 0,
      }
    })
  } catch {
    // 后端未就绪时退回本地默认
    classControls.value = classNames.map((name) => ({
      name,
      conf: 0.4,
      mode: name === 'TanChuang' ? 'disabled' : 'click',
      clicks: 0,
      failures: 0,
    }))
  }
}

// ============ 事件流 ============
const events = ref<any[]>([])
const selectedEvent = ref<any>(null)
const filter = ref({ action: '', cls: '' })
const eventListRef = ref<HTMLElement>()

const filteredEvents = computed(() => {
  return events.value.filter((e) => {
    if (filter.value.action && e.action !== filter.value.action) return false
    if (filter.value.cls && e.cls_name !== filter.value.cls && e.container_cls !== filter.value.cls) return false
    return true
  })
})

// ============ 画布绘制 ============
let unsub: (() => void) | null = null
let pollTimer: number | null = null
let rafId = 0

function renderCanvases() {
  const p = frame.value
  if (!p) return
  if (leftCanvas.value) drawScreenFrame(leftCanvas.value, p, app.layers, hoverKey.value)
  if (rightCanvas.value) drawModelFrame(rightCanvas.value, p, app.layers)
  if (roiCanvas.value) drawRoiFrame(roiCanvas.value, p)

  modelInputInfo.value = `imgsz=${p.imgsz} ratio=${p.ratio.toFixed(3)} pad=(${Math.round(p.dw)},${Math.round(p.dh)})`
  roiInfo.value = p.roi ? `${p.roi.w}×${p.roi.h}px → imgsz2=${p.roi.imgsz2}` : '无容器'
}

function scheduleRender() {
  if (rafId) return
  rafId = requestAnimationFrame(() => {
    rafId = 0
    renderCanvases()
  })
}

function onFrame(p: FramePayload) {
  frame.value = p
  scheduleRender()
}

async function startFrameSource() {
  // 先拉一次最近帧，避免等下一个事件才出画面
  try {
    const latest = await api.getLiveFrame()
    if (latest) onFrame(latest)
  } catch { /* 后端未就绪 */ }

  if (hasWailsEvents()) {
    unsub = onWailsFrame(onFrame)
    return
  }
  // 事件不可用时退回轮询（生产环境走 Wails 注入，一般不会到这里）
  pollTimer = window.setInterval(async () => {
    try {
      const p = await api.getLiveFrame()
      if (p && (!frame.value || p.ts !== frame.value.ts)) onFrame(p)
    } catch { /* 忽略 */ }
  }, 500)
}

// 叠加层开关变化时立即重绘（不依赖新帧）；state 提升到 store 后跨页面保持
watch(() => app.layers, () => scheduleRender(), { deep: true })

// ============ 运行控制 ============
async function toggleFreeze() {
  frozen.value = !frozen.value
  try {
    await api.freezeFrame(frozen.value)
    frameStats.value = await api.getFrameStats()
  } catch { /* 忽略 */ }
}

async function refreshStats() {
  try {
    frameStats.value = await api.getFrameStats()
  } catch { /* 忽略 */ }
}

// ============ 事件流 ============
async function loadEvents() {
  try {
    const rows = await api.getEvents(100, 0)
    events.value = rows || []
  } catch {
    events.value = []
  }
}

async function loadModelVersions() {
  try {
    const m: any = await api.getHeartbeat()
    modelInfo.value.lastMs = m?.total_ms || 0
    modelInfo.value.imgsz = m?.imgsz || frame.value?.imgsz || 0
    if (frame.value) {
      modelInfo.value.threads = modelInfo.value.threads || 0
    }
  } catch { /* 静默 */ }
}

function selectEvent(e: any) {
  selectedEvent.value = e
}

async function applyPolicy() {
  const updates: Record<string, { conf: number; mode: string }> = {}
  for (const c of classControls.value) {
    updates[c.name] = { conf: c.conf, mode: c.mode }
  }
  try {
    await api.saveCategoryPolicy(updates)
    classModeChanged.value = false
  } catch (e) {
    console.warn('[Inspector] 保存类别策略失败:', e)
  }
}

function singleClassRun(cls: string) {
  // 单类试跑：待后端接口
  hoverInfo.value = cls ? `单类试跑 ${cls}：接口待接入` : '请先在表格中选一个类别'
  window.setTimeout(() => { hoverInfo.value = '' }, 2500)
}

// ============ 悬停联动：屏幕坐标 ↔ 模型坐标 ============
function onLeftMove(e: MouseEvent) {
  const canvas = leftCanvas.value
  const p = frame.value
  if (!canvas || !p) return
  const rect = canvas.getBoundingClientRect()
  const cssX = e.clientX - rect.left
  const cssY = e.clientY - rect.top

  const hit: FrameBox | null = hitTest(p, canvas, cssX, cssY)
  hoverKey.value = hit ? `${hit.cls_name}:${Math.round(hit.box[0])},${Math.round(hit.box[1])}` : undefined

  // 反查：该像素在模型空间的位置（用屏幕坐标算，不依赖 fit）
  const fitK = Math.min(rect.width / p.img_w, rect.height / p.img_h)
  const dx = (rect.width - p.img_w * fitK) / 2
  const dy = (rect.height - p.img_h * fitK) / 2
  const imgX = (cssX - dx) / fitK
  const imgY = (cssY - dy) / fitK
  const [scrX, scrY] = [imgX / p.scale, imgY / p.scale]
  const [mX, mY] = screenToModel(p, scrX, scrY)

  const parts = [`屏幕 ${Math.round(scrX)},${Math.round(scrY)}px`, `模型 ${Math.round(mX)},${Math.round(mY)}px`]
  if (hit) parts.push(`${hit.cls_name} ${hit.conf.toFixed(2)}${hit.reject_reason ? ' ✗' + hit.reject_reason : ''}`)
  hoverInfo.value = parts.join('  |  ')

  scheduleRender()
}

function onLeftLeave() {
  hoverInfo.value = ''
  hoverKey.value = undefined
  scheduleRender()
}

// 事件流人类可读映射：类别名 / 动作 / 结果 中文化
const clsNameCN: Record<string, string> = {
  TanChuang: '弹窗', GuanBi: '关闭', GuanBi_str: '关闭(文字)', XiaYiBu: '下一步',
  QuXiao: '取消', TuiChu: '退出', TiaoGuo: '跳过', WoBuShou: '我接受',
  ZhiDaoLe: '知道了', YunXu: '允许',
}
function clsCN(name?: string) {
  return (name && clsNameCN[name]) || name || '—'
}

// 拒绝原因中文化（与引擎 RejectReason 取值对齐）
const reasonMap: Record<string, string> = {
  disabled_class: '该类别已被禁用',
  outside_container: '按钮不在弹窗窗口内',
  low_conf: '置信度过低，宁可不点',
  suppressed_rapid: '同一弹窗短时间重复出现，已抑制',
  no_container: '未找到弹窗本体',
  no_exit: '未检出关闭按钮（✕ 漏检或被遮挡）',
}
function reasonCN(r?: string) {
  return (r && reasonMap[r]) || r || ''
}

// 一行标题：直接回答"它干了什么 / 为什么没干"
function eventHeadline(e: any): string {
  const name = clsCN(e.cls_name)
  switch (e.action) {
    case 'click':
      return `已点击「${name}」按钮`
    case 'report_only':
      // 有明确拒因（含 no_exit）或未过闸 → 说清"为什么没点"
      if (e.reject_reason || !e.gate_passed) {
        return `发现「${name}」，未点击（${reasonCN(e.reject_reason) || '条件不满足'}）`
      }
      // 过闸但仍只上报：观察模式=设计如此；自动模式=等待点击条件（多帧确认已移除）
      return app.currentMode === 'auto'
        ? '目标已确认，准备点击'
        : '发现弹窗，仅记录不点击（观察模式）'
    case 'blocked_user_active':
      return '您正在操作电脑，本次不介入'
    case 'blocked_forbidden_zone':
      return '处于禁区（任务栏等），不介入'
    case 'suppressed_rapid':
      return '重复弹窗，已抑制不重复处理'
    case 'low_conf':
      return `疑似「${name}」，置信度过低不处理`
    default:
      return `检测到「${name}」`
  }
}

// 第二行：位置数据、落点、验证结果等细节
function eventDetail(e: any): string {
  const parts: string[] = []
  if (e.container_x2 != null) {
    const cw = Math.round(e.container_x2 - e.container_x1)
    const ch = Math.round(e.container_y2 - e.container_y1)
    parts.push(`弹窗「${clsCN(e.container_cls)}」位置(${Math.round(e.container_x1)},${Math.round(e.container_y1)}) 大小${cw}×${ch} 置信${pct(e.container_conf)}`)
  }
  if (e.cls_name && e.role === 'exit' && (e.action === 'click' || e.action === 'report_only')) {
    parts.push(`目标「${clsCN(e.cls_name)}」中心(${Math.round(e.cx)},${Math.round(e.cy)}) 置信${pct(e.conf)}`)
  }
  if (e.click_x != null) {
    parts.push(`实际落点(${Math.round(e.click_x)},${Math.round(e.click_y)})`)
  }
  if (e.click_verify) {
    parts.push(verifyCN(e.click_verify))
  }
  if (e.click_retry > 0) {
    parts.push(`重试${e.click_retry}次`)
  }
  if (e.capture_ms != null && e.model_ms != null) {
    // model_ms=0 表示一级检测已直接命中目标，二级级联未触发
    parts.push(`截屏${e.capture_ms}ms + ${e.model_ms > 0 ? `推理${e.model_ms}ms` : '一级已检出'}`)
  }
  return parts.join(' · ') || `帧#${e.frame_index} · ${e.box_count ?? 0}个目标框`
}

function pct(v?: number) {
  return v == null ? '—' : `${Math.round(v * 100)}%`
}

function verifyCN(v: string): string {
  const map: Record<string, string> = {
    success: '点击有效：弹窗已消失',
    failed: '点击无效：弹窗仍在',
    pending: '验证中',
  }
  return map[v] || `验证结果:${v}`
}

// 第三段小字：点击类事件的最终结论（成功/未关闭）
function verdictCN(e: any): string {
  if (e.action !== 'click') return ''
  if (e.click_verify === 'success' || e.result === 'success') return '✓ 弹窗已关闭'
  if (e.click_verify === 'failed' || e.result === 'failed') return '✗ 弹窗未关闭'
  return ''
}
function verdictColor(e: any): string {
  if (verdictCN(e).startsWith('✓')) return 'green'
  if (verdictCN(e).startsWith('✗')) return 'red'
  return ''
}

function formatTime(ts: string) {
  return new Date(ts).toLocaleTimeString('zh-CN', { hour12: false })
}

function eventColor(e: any) {
  if (e.action === 'click') return 'green'
  if (e.result === 'failed') return 'red'
  if (e.action?.startsWith('blocked')) return 'blue'
  return ''
}

function actionColor(action: string) {
  if (action === 'click') return 'green'
  if (action?.startsWith('blocked')) return 'blue'
  if (action === 'low_conf') return 'orange'
  if (action === 'report_only') return 'gray'
  return ''
}

function modeDotClass(mode: string) {
  return mode === 'click' ? 'dot-green' : mode === 'report' ? 'dot-blue' : 'dot-gray'
}

onMounted(async () => {
  await nextTick()
  loadEvents()
  loadModelVersions()
  loadCategoryPolicy()
  startFrameSource()
  window.addEventListener('resize', scheduleRender)
})

onUnmounted(() => {
  unsub?.()
  if (pollTimer !== null) window.clearInterval(pollTimer)
  if (rafId) cancelAnimationFrame(rafId)
  window.removeEventListener('resize', scheduleRender)
})
</script>

<style scoped>
/* ==================== 三栏骨架：左栏 240px / 中区 flex / 右抽屉 360px ==================== */
.inspector {
  height: 100%;
  display: flex;
  gap: 8px;
  padding: 8px;
  overflow: hidden;
  min-height: 0; /* 红线: flex 子元素必须允许收缩 */
}

/* ---------- 左栏 ---------- */
.left-panel {
  width: 240px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  overflow-y: auto;
}

.lp-card {
  background: var(--color-bg-card);
  border: 1px solid var(--color-border);
  border-radius: var(--radius);
  padding: 10px 12px;
  flex-shrink: 0;
}

.lp-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--color-text-dim);
  margin-bottom: 8px;
  letter-spacing: 0.5px;
}

.lp-row {
  display: flex;
  justify-content: space-between;
  font-size: 12px;
  padding: 3px 0;
}

.lp-label { color: var(--color-text-dim); }
.lp-value { color: var(--color-text); }

.ellipsis {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 140px;
}

.cls-quick {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 2px 0;
  font-size: 12px;
}

.cls-quick-label {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cls-quick-name { font-size: 8px; }
.conf-input-sm {
  width: 56px;
  padding: 2px 4px;
  font-size: 12px;
  text-align: center;
  border: 1px solid var(--color-border, #ccc);
  border-radius: 4px;
  background: transparent;
  color: inherit;
  flex-shrink: 0;
}
.dot-green { color: var(--color-green); }
.dot-blue { color: var(--color-blue); }
.dot-gray { color: var(--color-gray); }

.lp-btn-group {
  display: flex;
  gap: 6px;
  margin-bottom: 6px;
}

.lp-btn {
  flex: 1;
  font-size: 12px;
  padding: 5px 8px;
  background: var(--color-bg-hover);
}

.lp-btn.full { width: 100%; margin-top: 6px; }
.lp-btn.active { background: #1a4a80; }
.lp-btn.primary { background: var(--color-blue); color: #fff; }

.lp-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 6px;
}

/* ---------- 中区 ---------- */
.center-panel {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 8px;
  overflow: hidden;
  min-width: 0; /* 红线: flex 子元素必须允许收缩 */
  min-height: 0;
}

.canvas-row {
  display: flex;
  gap: 8px;
  height: 260px;
  flex-shrink: 0;
}

.canvas-panel {
  flex: 1;
  display: flex;
  flex-direction: column;
  background: var(--color-bg-card);
  border-radius: var(--radius);
  border: 1px solid var(--color-border);
  overflow: hidden;
  min-width: 0; /* 红线: canvas 有固有宽度，必须允许收缩 */
}

.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 12px;
  background: #111;
  font-size: 12px;
  border-bottom: 1px solid var(--color-border);
  flex-shrink: 0;
}

.ph-title {
  font-weight: 600;
}

.layer-toggles {
  display: flex;
  gap: 10px;
  font-size: 11px;
}

.layer-toggles label {
  display: flex;
  align-items: center;
  gap: 3px;
}

.canvas-wrap {
  flex: 1;
  position: relative;
  background: #000;
  min-height: 0;
}

.inspector-canvas {
  width: 100%;
  height: 100%;
  display: block;
}

.canvas-empty {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-dim);
  pointer-events: none;
}

.hover-tip {
  position: absolute;
  bottom: 6px;
  left: 6px;
  background: rgba(0, 0, 0, 0.75);
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 11px;
  pointer-events: none;
}

.roi-panel {
  width: 200px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  background: var(--color-bg-card);
  border-radius: var(--radius);
  border: 1px solid var(--color-border);
  overflow: hidden;
}

.roi-preview {
  flex: 1;
  background: #000;
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 0;
}

.roi-info {
  padding: 4px 8px;
  background: #111;
  flex-shrink: 0;
}

/* ---------- 逐类控制表 ---------- */
.class-control-table {
  flex-shrink: 0;
  padding: 8px 12px;
}

table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}

th {
  text-align: left;
  padding: 3px 8px;
  color: var(--color-text-dim);
  font-weight: 400;
  border-bottom: 1px solid var(--color-border);
}

td {
  padding: 4px 8px;
  border-bottom: 1px solid #1a1a2e;
}

.cls-disabled td { opacity: 0.45; }

.cls-name { font-weight: 600; }

.conf-slider { width: 90px; }
.conf-val { margin-left: 6px; color: var(--color-text-dim); }

.mode-select {
  background: #1a1a2e;
  color: var(--color-text);
  border: 1px solid var(--color-border);
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 12px;
}

.table-actions {
  padding-top: 6px;
  text-align: right;
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 8px;
}

.text-btn {
  background: transparent;
  color: var(--color-blue);
  font-size: 12px;
  padding: 2px 8px;
}

/* ---------- 事件流（主角） ---------- */
.event-stream {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
  padding: 0;
}

.event-filters {
  display: flex;
  gap: 8px;
}

.filter-select {
  background: #1a1a2e;
  color: var(--color-text);
  border: 1px solid var(--color-border);
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 12px;
}

.event-list {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}

.event-empty {
  padding: 24px;
  text-align: center;
}

.event-row {
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding: 5px 12px;
  font-size: 12px;
  cursor: pointer;
  border-bottom: 1px solid #1a1a2e;
}

.event-row:hover { background: rgba(255, 255, 255, 0.03); }

.event-row.green { border-left: 3px solid var(--color-green); }
.event-row.red { border-left: 3px solid var(--color-red); }
.event-row.blue { border-left: 3px solid var(--color-blue); }

.ev-line1 {
  display: flex;
  align-items: center;
  gap: 10px;
}

.ev-time { width: 72px; color: var(--color-text-dim); flex-shrink: 0; }
.ev-head { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ev-verdict { flex-shrink: 0; font-weight: 600; }
.ev-ms { width: 55px; text-align: right; color: var(--color-text-dim); flex-shrink: 0; }

.ev-line2 {
  padding-left: 82px;
  font-size: 11px;
  color: var(--color-text-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.green { color: var(--color-green); }
.red { color: var(--color-red); }
.blue { color: var(--color-blue); }
.orange { color: var(--color-orange); }
.gray { color: var(--color-gray); }
.yellow { color: var(--color-yellow); }

/* ---------- 右栏抽屉 ---------- */
.detail-drawer {
  width: 360px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: 0;
}

.detail-content {
  flex: 1;
  padding: 12px;
  min-height: 0;
}

.detail-section { margin-bottom: 12px; }

.detail-label {
  font-size: 11px;
  color: var(--color-text-dim);
  margin-bottom: 4px;
}

.box-list {
  background: #0d1117;
  padding: 8px;
  border-radius: 4px;
  font-size: 11px;
  overflow-x: auto;
}
</style>
