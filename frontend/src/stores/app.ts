import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api, type Heartbeat } from '@/api'

export const useAppStore = defineStore('app', () => {
  const isMaintainer = ref(true)
  const isAuthorized = ref(false)
  const currentMode = ref('observe')
  const heartbeat = ref<Partial<Heartbeat>>({})
  const frozen = ref(false)
  const lastUndoTime = ref<Date | null>(null)

  // 调试台画布叠加层开关（容器/出口/被拒/点击点/轨迹）。
  // 提升到 store：跨页面导航（调试台↔监控台↔设置）后勾选状态不丢失（即跟即随）。
  const layers = ref({
    container: true,
    exit: true,
    rejected: true,
    click: true,
    trace: false,
  })

  const modeLabel = computed(() => {
    const labels: Record<string, string> = {
      unauthorized: '未授权',
      selftest_pending: '自检未通过',
      observe: '观察模式',
      auto: '自动模式',
      extreme: '极限模式',
      paused: '已暂停',
      degraded: '已降级',
    }
    return labels[currentMode.value] || currentMode.value
  })

  const modeColor = computed(() => {
    const colors: Record<string, string> = {
      unauthorized: 'red',
      selftest_pending: 'orange',
      observe: 'gray',
      auto: 'blue',
      extreme: 'red',
      paused: 'gray',
      degraded: 'red',
    }
    return colors[currentMode.value] || 'gray'
  })

  // isAutoMode：会真实点击的模式（自动 + 极限）
  const isAutoMode = computed(() => currentMode.value === 'auto' || currentMode.value === 'extreme')
  const isExtremeMode = computed(() => currentMode.value === 'extreme')

  async function fetchHeartbeat() {
    try {
      const hb = await api.getHeartbeat()
      heartbeat.value = hb
      currentMode.value = hb.mode || 'observe'
      isAuthorized.value = Boolean(hb.screen_authorized && hb.accessibility_authorized)
    } catch (err) {
      console.warn('[ScreenGuard] 获取心跳失败:', err)
    }
  }

  let switchingMode = false
  async function setMode(mode: string) {
    if ((mode === 'auto' || mode === 'extreme') && !isMaintainer.value) return
    if (switchingMode) return // 防抖：上一次切换还没完成，忽略连点
    switchingMode = true
    const prev = currentMode.value
    currentMode.value = mode // 乐观更新，失败回滚
    try {
      await api.setMode(mode)
      await fetchHeartbeat()
    } catch (err) {
      console.warn('[ScreenGuard] 切换模式失败，已回滚:', err)
      currentMode.value = prev
    } finally {
      switchingMode = false
    }
  }

  async function pause(duration: number) {
    await api.pause(duration)
    currentMode.value = 'paused'
    await fetchHeartbeat()
  }

  async function resume() {
    await api.resume()
    await fetchHeartbeat()
  }

  function markUndo() {
    lastUndoTime.value = new Date()
  }

  return {
    isMaintainer,
    isAuthorized,
    currentMode,
    heartbeat,
    frozen,
    lastUndoTime,
    layers,
    modeLabel,
    modeColor,
    isAutoMode,
    isExtremeMode,
    fetchHeartbeat,
    setMode,
    pause,
    resume,
    markUndo,
  }
})
