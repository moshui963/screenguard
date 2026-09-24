<template>
  <div class="onboarding">
    <div class="onboarding-card">
      <div class="ob-header">
        <h1>屏净 ScreenGuard</h1>
        <p class="text-dim">自动识别并关闭屏幕弹窗，让桌面保持干净</p>
      </div>

      <!-- 步骤指示 -->
      <div class="steps">
        <div v-for="(s, i) in stepLabels" :key="i"
          class="step" :class="{ active: step === i, done: step > i }">
          <div class="step-num">{{ step > i ? '✓' : i + 1 }}</div>
          <div class="step-label">{{ s }}</div>
        </div>
      </div>

      <!-- Step 0: 权限引导 -->
      <div class="ob-step" v-if="step === 0">
        <div class="notice text-sm text-dim">
          只保留并授权 <b>屏净 ScreenGuard.app</b>。不要授权或启动根目录下的裸二进制 <code>screenguard</code>。
        </div>

        <div class="perm-card" v-for="p in permissions" :key="p.key">
          <div class="perm-status">
            <span class="perm-dot" :class="dotClass(p)">●</span>
            <span class="perm-name">{{ p.name }}</span>
            <span class="perm-state text-sm" :class="stateTextClass(p)">{{ stateText(p) }}</span>
          </div>
          <div class="perm-desc text-dim text-sm">{{ p.desc }}</div>

          <div class="perm-actions">
            <button class="primary" @click="requestPerm(p)" v-if="p.state !== 'granted'">
              请求授权
            </button>
            <button @click="openSystemPref(p.url)" v-if="p.state !== 'granted'">
              打开系统设置
            </button>
          </div>
        </div>

        <!-- 屏幕录制三态提示（红线 A2/A4） -->
        <div v-if="screenState === 'needs_restart'" class="restart-banner">
          <div class="restart-title">⚠ 权限已授予，但需要重启应用才能生效</div>
          <p class="text-sm text-dim">
            macOS 的屏幕录制权限对正在运行的进程不生效。请在系统设置中勾选后，重启 ScreenGuard。
          </p>
          <button class="primary" @click="restartApp">重启 ScreenGuard</button>
        </div>

        <!-- 授权轮询进行中 -->
        <div v-if="polling" class="text-sm text-dim poll-hint">
          正在等待授权…（去系统设置勾选后会自动检测，无需手动刷新）
        </div>

        <div class="actions">
          <button @click="refreshPermissions">重新检查</button>
          <button class="primary" @click="step = 1" :disabled="!allRequiredGranted">下一步</button>
        </div>
        <p v-if="message" class="text-sm" :class="allRequiredGranted ? 'green' : 'orange'">{{ message }}</p>
      </div>

      <!-- Step 1: 抓屏能力实测 -->
      <div class="ob-step" v-if="step === 1">
        <div class="capture-test">
          <h3>抓屏能力实测</h3>
          <p class="text-dim text-sm">
            后端会真实截取一帧并检测像素方差（不是只读权限开关），验证授权确实生效。
          </p>
          <div class="capture-preview" v-if="captureTested">
            <div v-if="captureOK" class="green">✓ 抓屏正常，画面有效</div>
            <div v-else class="capture-fail red">
              抓屏失败或画面全黑。可能原因：授权未生效（需重启应用）、屏幕受保护。
            </div>
          </div>
          <button class="primary" @click="testCapture">测试抓屏</button>
          <button @click="step = 2" v-if="captureTested && captureOK">下一步</button>
        </div>
      </div>

      <!-- Step 2: 自检执行 -->
      <div class="ob-step" v-if="step === 2">
        <div class="selftest-run">
          <h3>自检执行</h3>
          <p class="text-dim text-sm">在合成探针图上跑各档分辨率，约 3~5 秒</p>
          <div class="progress-bar-wrap" v-if="testing">
            <div class="progress-bar" :style="{ width: progress + '%' }"></div>
          </div>
          <div class="text-sm text-dim" v-if="testing">{{ testProgressText }}</div>
          <div v-if="selftestError" class="red text-sm">{{ selftestError }}</div>
          <button class="primary" @click="runSelfTest" v-if="!testing && !testDone">运行自检</button>
          <div v-if="testDone" class="test-result">
            <span :class="testPassed ? 'green' : 'orange'">
              {{ testPassed ? '✓ 自检通过' : '✗ 自检未通过，所有类别降级为只上报' }}
            </span>
          </div>
          <button class="primary" @click="finish" v-if="testDone">完成，进入监控台</button>
          <button @click="skipSelfTest" v-if="!testing">跳过（先用观察模式）</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api'

const router = useRouter()
const step = ref(0)
const stepLabels = ['权限引导', '抓屏实测', '自检执行']
const captureTested = ref(false)
const captureOK = ref(false)
const testing = ref(false)
const progress = ref(0)
const testProgressText = ref('')
const testDone = ref(false)
const testPassed = ref(false)
const selftestError = ref('')
const message = ref('')

// 屏幕权限三态（红线 A2）: unauthorized / needs_restart / ok
const screenState = ref<'unauthorized' | 'needs_restart' | 'ok'>('unauthorized')
const accessibilityGranted = ref(false)
const polling = ref(false)
let pollTimer: number | null = null

const permissions = computed(() => [
  {
    key: 'screen', name: '屏幕录制',
    desc: '用于抓取屏幕画面。授权后需重启应用生效。',
    url: 'x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture',
    state: screenState.value === 'ok' ? 'granted'
      : screenState.value === 'needs_restart' ? 'needs_restart' : 'denied',
  },
  {
    key: 'accessibility', name: '辅助功能',
    desc: '用于模拟鼠标点击关闭弹窗。',
    url: 'x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility',
    state: accessibilityGranted.value ? 'granted' : 'denied',
  },
])

const allRequiredGranted = computed(() =>
  screenState.value === 'ok' && accessibilityGranted.value)

onMounted(() => {
  refreshPermissions()
})

onUnmounted(() => {
  stopPolling()
})

function stateText(p: { state: string }): string {
  if (p.state === 'granted') return '✓ 已授权'
  if (p.state === 'needs_restart') return '⚠ 已授权，待重启生效'
  return '未授权'
}
function stateTextClass(p: { state: string }): string {
  return p.state === 'granted' ? 'green' : p.state === 'needs_restart' ? 'orange' : 'red'
}
function dotClass(p: { state: string }): string {
  return p.state === 'granted' ? 'green' : p.state === 'needs_restart' ? 'orange' : 'red'
}

function openSystemPref(url: string) {
  window.open(url, '_self')
}

// A2: 持续轮询直到授权成功（1.5s 间隔），替代原来"1 秒后查一次就放弃"
function startPolling() {
  if (pollTimer !== null) return
  polling.value = true
  pollTimer = window.setInterval(async () => {
    await refreshPermissions()
    if (allRequiredGranted.value) {
      stopPolling()
      message.value = '权限全部就绪，可以继续。'
    }
  }, 1500)
}
function stopPolling() {
  polling.value = false
  if (pollTimer !== null) {
    window.clearInterval(pollTimer)
    pollTimer = null
  }
}

async function refreshPermissions() {
  try {
    const status: any = await api.getPermissions()
    // 三态: unauthorized / needs_restart / ok（红线 A4: 后端已做像素方差实测）
    screenState.value = (status.screen_state as typeof screenState.value) || 'unauthorized'
    accessibilityGranted.value = Boolean(status.accessibility)

    if (allRequiredGranted.value) {
      message.value = '权限全部就绪。'
    } else if (screenState.value === 'needs_restart') {
      message.value = '屏幕录制权限已授予，重启应用后生效。'
    } else {
      message.value = '请在系统设置中勾选对应权限，本页会自动检测。'
    }
  } catch (err) {
    message.value = `权限检查失败：${err}`
  }
}

async function requestPerm(p: { key: string; url: string }) {
  try {
    // 注意（红线 A2）: requestScreenCapturePermission 的返回值是"是否发起了请求"，
    // 不是授权结果。授权结果靠轮询 getPermissions 获得。
    if (p.key === 'screen') {
      await api.requestScreenCapturePermission()
    } else if (p.key === 'accessibility') {
      await api.requestAccessibilityPermission()
    }
  } catch {
    // 忽略：部分系统上请求 API 会直接弹系统对话框
  } finally {
    openSystemPref(p.url)
    startPolling()
  }
}

async function testCapture() {
  captureTested.value = true
  await refreshPermissions()
  // A4: 后端 screen_state=ok 意味着真实截帧且像素方差达标
  captureOK.value = screenState.value === 'ok'
}

async function runSelfTest() {
  testing.value = true
  progress.value = 0
  selftestError.value = ''
  const stages = ['生成合成探针…', 'imgsz=1024…', 'imgsz=1280…', 'imgsz=1536…', '分析结果…']
  for (let i = 0; i < stages.length - 1; i++) {
    testProgressText.value = stages[i]
    progress.value = ((i + 1) / stages.length) * 100
    await new Promise(r => setTimeout(r, 400))
  }
  testProgressText.value = stages[stages.length - 1]
  progress.value = 100
  try {
    await api.runSelfTest()
    testPassed.value = true
  } catch (err: any) {
    testPassed.value = false
    selftestError.value = `自检失败：${err?.message || err}`
  } finally {
    testing.value = false
    testDone.value = true
  }
}

// A3: 重启闭环 — 退出并由登录项/用户手动拉起
async function restartApp() {
  // 后端会以 bundle 身份重新拉起 ScreenGuard.app 再退出旧进程，
  // 保证 TCC 授权身份一致（裸二进制拿不到 .app 的授权）。
  try {
    await api.restartApp()
  } catch {
    message.value = '请手动退出后重新打开 ScreenGuard.app（不要开根目录的裸二进制）。'
  }
}

function skipSelfTest() {
  router.push('/')
}

function finish() {
  router.push('/')
}
</script>

<style scoped>
.onboarding { height: 100vh; display: flex; align-items: center; justify-content: center; background: var(--color-bg); }
.onboarding-card { width: 540px; max-height: 90vh; overflow-y: auto; background: var(--color-bg-card); border-radius: 12px; padding: 32px; border: 1px solid var(--color-border); }
.ob-header { text-align: center; margin-bottom: 24px; }
.ob-header h1 { font-size: 24px; }
.ob-header p { margin-top: 8px; }
.steps { display: flex; justify-content: space-between; margin-bottom: 32px; }
.step { display: flex; flex-direction: column; align-items: center; gap: 4px; flex: 1; }
.step-num { width: 28px; height: 28px; border-radius: 50%; background: #333; display: flex; align-items: center; justify-content: center; font-size: 14px; color: #888; }
.step.active .step-num { background: var(--color-blue); color: #fff; }
.step.done .step-num { background: var(--color-green); color: #000; }
.step-label { font-size: 11px; color: var(--color-text-dim); }
.step.active .step-label { color: #fff; }
.ob-step { min-height: 200px; }
.notice { padding: 12px; background: rgba(96,165,250,0.1); border: 1px solid rgba(96,165,250,0.25); border-radius: 8px; margin-bottom: 12px; line-height: 1.7; }
.perm-card { padding: 16px; border: 1px solid var(--color-border); border-radius: 8px; margin-bottom: 12px; }
.perm-status { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
.perm-state { margin-left: auto; }
.perm-dot { font-size: 8px; }
.perm-name { font-weight: 600; }
.perm-desc { margin-bottom: 8px; }
.perm-actions { display: flex; gap: 8px; }
.actions { display: flex; gap: 8px; margin: 16px 0; }
.restart-banner { padding: 16px; background: rgba(251,146,60,0.12); border: 1px solid rgba(251,146,60,0.35); border-radius: 8px; margin-bottom: 12px; }
.restart-title { font-weight: 600; margin-bottom: 8px; color: var(--color-orange); }
.poll-hint { margin-top: 8px; }
.capture-test { text-align: center; padding: 24px; }
.capture-preview { margin: 16px 0; }
.capture-fail { padding: 16px; background: rgba(248,113,113,0.1); border-radius: 6px; }
.selftest-run { text-align: center; padding: 24px; }
.progress-bar-wrap { width: 100%; height: 8px; background: #222; border-radius: 4px; overflow: hidden; margin: 16px 0; }
.progress-bar { height: 100%; background: var(--color-blue); transition: width 0.3s; }
.test-result { margin: 16px 0; font-size: 16px; }
.green { color: var(--color-green); }
.red { color: var(--color-red); }
.orange { color: var(--color-orange); }
.text-dim { color: var(--color-text-dim); }
.text-sm { font-size: 12px; }
</style>
