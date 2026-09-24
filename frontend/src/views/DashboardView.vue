<template>
  <div class="dashboard scroll-y">
    <div class="dashboard-content">
      <!-- 指标卡片：单行排列在实时监控上方，不换行 -->
      <div class="metric-strip">
        <div class="metric-chip">
          <span class="m-value">{{ totalSeen }}</span>
          <span class="m-label">弹窗总数</span>
        </div>
        <div class="metric-chip">
          <span class="m-value blue">{{ hb.today_blocked ?? 0 }}</span>
          <span class="m-label">自动关闭</span>
        </div>
        <div class="metric-chip">
          <span class="m-value red">{{ hb.today_false ?? 0 }}</span>
          <span class="m-label">误点</span>
        </div>
        <div class="metric-chip">
          <span class="m-value green">{{ (hb.actual_fps ?? 0).toFixed(1) }}</span>
          <span class="m-label">实时 FPS</span>
        </div>
        <button class="metric-chip mode-toggle" :class="'dot-' + app.modeColor"
                @click="toggleMode" title="点击在 观察模式 / 自动模式 间切换">
          <span class="m-value">{{ app.modeLabel }}</span>
          <span class="m-label">当前模式 · 点击切换</span>
        </button>
        <button class="big-btn primary sm" @click="goInspector" v-if="app.isMaintainer">打开详情</button>
      </div>

      <!-- "活着"的证据 — 最近一帧缩略图 -->
      <div class="card preview-card">
        <div class="card-title">实时监控</div>
        <div class="preview-container">
          <canvas ref="previewCanvas" class="preview-canvas"></canvas>
          <div class="preview-overlay" v-if="!hasData">
            <span class="text-dim">等待抓屏数据…（需授予屏幕录制权限并启动检测）</span>
          </div>
          <div class="preview-fps" v-if="hasData">
            {{ (hb.actual_fps ?? 0).toFixed(1) }} fps · {{ hb.last_capture_ago }}
          </div>
        </div>
        <div class="preview-hint text-sm text-dim" v-if="hasData">
          绿框=出口(可点) · 橙框=容器 · 白十字=命中位置 · 观察模式仅悬停不点，自动模式会真实点击
        </div>
      </div>

      <!-- 操作按钮区 -->
      <div class="action-buttons">
        <button class="big-btn" @click="doPause">
          {{ app.currentMode === 'paused' ? '恢复监控' : '暂停 10 分钟' }}
        </button>
        <button
          class="big-btn extreme-btn"
          :class="{ active: app.isExtremeMode }"
          :title="app.isExtremeMode ? '点击退出极限模式（回到自动模式）' : '开启后不再理会用户是否在操作，检测到关闭目标立即点击'"
          @click="toggleExtreme"
        >
          ⚡ 极限模式{{ app.isExtremeMode ? ' · 已开启' : '' }}
        </button>
      </div>
      <div class="extreme-hint text-sm text-dim" v-if="app.isExtremeMode">
        ⚡ 极限模式运行中：无视用户操作，检测到弹窗立即点击。误点风险高，用完请及时关闭。
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { api, onWailsFrame, hasWailsEvents, type FramePayload } from '@/api'
import { drawScreenFrame, type LayerFlags } from '@/utils/frameDraw'

const router = useRouter()
const app = useAppStore()
const previewCanvas = ref<HTMLCanvasElement>()
const hasData = ref(false)

const topApps = ref<any[]>([])
const recentErrors = ref<any[]>([])

const hb = computed(() => app.heartbeat || {})
const totalSeen = computed(() => (hb.value.today_reported ?? 0) + (hb.value.today_blocked ?? 0))

// 监控画布叠加层：容器 + 出口 + 命中十字，隐藏"被拒"减噪
const liveLayers: LayerFlags = {
  container: true,
  exit: true,
  rejected: false,
  click: true,
  trace: false,
}

let unsub: (() => void) | null = null
let frame = ref<FramePayload | null>(null)
let rafId = 0

function renderPreview() {
  const p = frame.value
  const cv = previewCanvas.value
  if (!p || !cv) return
  drawScreenFrame(cv, p, liveLayers)
}

function scheduleRender() {
  if (rafId) return
  rafId = requestAnimationFrame(() => {
    rafId = 0
    renderPreview()
  })
}

function onFrame(p: FramePayload) {
  frame.value = p
  hasData.value = true
  scheduleRender()
}

async function startFrameSource() {
  try {
    const latest = await api.getLiveFrame()
    if (latest) onFrame(latest)
  } catch { /* 后端未就绪 */ }

  if (hasWailsEvents()) {
    unsub = onWailsFrame(onFrame)
    return
  }
  // 退化：轮询
  const timer = window.setInterval(async () => {
    try {
      const p = await api.getLiveFrame()
      if (p && (!frame.value || p.ts !== frame.value.ts)) onFrame(p)
    } catch { /* 忽略 */ }
  }, 500)
  onUnmounted(() => clearInterval(timer))
}

function barWidth(seen: number) {
  const max = Math.max(...topApps.value.map((a) => a.times_seen), 1)
  return (seen / max) * 100
}

function formatTime(ts: string) {
  const d = new Date(ts)
  return d.toLocaleString('zh-CN', { hour: '2-digit', minute: '2-digit', day: '2-digit' })
}

function doPause() {
  if (app.currentMode === 'paused') {
    app.resume()
  } else {
    app.pause(10)
  }
}

// 点击"当前模式"卡片：在 观察 / 自动 之间切换（即点即切换，无需进设置页）
function toggleMode() {
  app.setMode(app.isAutoMode ? 'observe' : 'auto')
}

// 极限模式：开启 = 切到 extreme；关闭 = 回到自动模式
function toggleExtreme() {
  app.setMode(app.isExtremeMode ? 'auto' : 'extreme')
}

function goInspector() {
  router.push('/inspector')
}

function markUndo(id: number) {
  app.markUndo()
}

onMounted(async () => {
  await startFrameSource()
  // 首次渲染（若画布尺寸已就绪）
  scheduleRender()
  window.addEventListener('resize', scheduleRender)
})

onUnmounted(() => {
  unsub?.()
  if (rafId) cancelAnimationFrame(rafId)
  window.removeEventListener('resize', scheduleRender)
})
</script>

<style scoped>
.dashboard {
  height: 100%;
}

.dashboard-content {
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 1200px;
  margin: 0 auto;
}

/* 单行指标条：不换行，横向滚动兜底 */
.metric-strip {
  display: flex;
  align-items: stretch;
  gap: 12px;
  overflow-x: auto;
  padding-bottom: 2px;
}

.metric-chip {
  flex: 1 0 auto;
  min-width: 96px;
  background: var(--color-bg-card);
  border: 1px solid var(--color-border);
  border-radius: var(--radius);
  padding: 10px 14px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
}

.m-value {
  font-size: 22px;
  font-weight: 700;
  line-height: 1.1;
  white-space: nowrap;
}

.m-label {
  font-size: 11px;
  color: var(--color-text-dim);
}

.big-btn.primary.sm {
  flex: 0 0 auto;
  padding: 0 16px;
  align-self: stretch;
}

/* "当前模式"卡片可点击切换观察/自动 */
.mode-toggle {
  cursor: pointer;
  border-color: var(--color-blue);
  font: inherit;
  color: var(--color-text);
  text-align: center;
  transition: background 0.15s, box-shadow 0.15s;
}
.mode-toggle:hover {
  background: var(--color-bg-hover);
  box-shadow: 0 0 0 1px var(--color-blue);
}
.mode-toggle:active { transform: translateY(1px); }

.preview-card {
  width: 100%;
}

.card-title {
  font-size: 13px;
  color: var(--color-text-dim);
  margin-bottom: 12px;
}

.preview-container {
  position: relative;
  background: #000;
  border-radius: 6px;
  overflow: hidden;
  aspect-ratio: 16/9;
  width: 100%;
}

.preview-canvas {
  width: 100%;
  height: 100%;
  display: block;
  object-fit: contain;
}

.preview-overlay {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: 0 16px;
}

.preview-fps {
  position: absolute;
  top: 8px;
  right: 8px;
  background: rgba(0, 0, 0, 0.6);
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 11px;
}

.preview-hint {
  margin-top: 8px;
}

.app-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.app-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}

.app-rank {
  width: 20px;
  color: var(--color-text-dim);
}

.app-name {
  width: 120px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.app-bar-wrap {
  flex: 1;
  height: 8px;
  background: #222;
  border-radius: 4px;
  overflow: hidden;
}

.app-bar {
  height: 100%;
  background: var(--color-blue);
  border-radius: 4px;
}

.app-count {
  width: 30px;
  text-align: right;
  font-weight: 600;
}

.empty-state {
  padding: 24px;
  text-align: center;
}

.error-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 0;
  border-bottom: 1px solid var(--color-border);
}

.error-time {
  color: var(--color-text-dim);
  font-size: 12px;
  width: 100px;
}

.error-desc {
  flex: 1;
}

.text-btn {
  background: transparent;
  color: var(--color-orange);
  font-size: 12px;
  padding: 2px 8px;
}

.action-buttons {
  display: flex;
  gap: 12px;
}

.big-btn {
  flex: 1;
  padding: 16px;
  font-size: 16px;
  font-weight: 600;
}

.extreme-btn {
  border: 2px solid var(--color-red);
  color: var(--color-red);
  background: transparent;
  border-radius: 8px;
  transition: all 0.15s ease;
}

.extreme-btn:hover {
  background: rgba(255, 59, 48, 0.08);
}

.extreme-btn.active {
  background: var(--color-red);
  color: #fff;
  animation: extreme-pulse 1.2s ease-in-out infinite;
}

@keyframes extreme-pulse {
  0%, 100% { box-shadow: 0 0 0 0 rgba(255, 59, 48, 0.4); }
  50% { box-shadow: 0 0 0 8px rgba(255, 59, 48, 0); }
}

.extreme-hint {
  margin-top: 8px;
  color: var(--color-red);
}

.dot-green { color: var(--color-green); }
.dot-blue { color: var(--color-blue); }
.dot-gray { color: var(--color-gray); }
.dot-red { color: var(--color-red); }
</style>
