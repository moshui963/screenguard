<template>
  <div class="settings-view scroll-y">
    <div class="settings-grid">
      <!-- 左列 -->
      <div class="col">
        <div class="card">
          <div class="card-title">运行模式</div>
          <div class="mode-section">
            <label class="mode-option">
              <input type="radio" v-model="config.mode" value="observe" @change="onModeChange" />
              <div>
                <span class="mode-name">观察模式</span>
                <span class="mode-desc text-dim text-sm">只检测记录，悬停不点击</span>
              </div>
            </label>
            <label class="mode-option" :class="{ disabled: !app.isMaintainer }">
              <input type="radio" v-model="config.mode" value="auto"
                :disabled="!app.isMaintainer" @change="onModeChange" />
              <div>
                <span class="mode-name">自动模式</span>
                <span class="mode-desc text-dim text-sm">检测到后自动关闭，您操作电脑时不介入</span>
              </div>
            </label>
            <label class="mode-option extreme" :class="{ disabled: !app.isMaintainer }">
              <input type="radio" v-model="config.mode" value="extreme"
                :disabled="!app.isMaintainer" @change="onModeChange" />
              <div>
                <span class="mode-name">⚡ 极限模式</span>
                <span class="mode-desc text-dim text-sm">无视用户操作，检测到立即点击，误点风险高</span>
              </div>
            </label>
          </div>
        </div>

        <div class="card mt-4">
          <div class="card-title">性能</div>
          <div class="setting-row">
            <span class="setting-label">性能档位</span>
            <select v-model="config.performance_tier" class="select">
              <option value="powersave">省电</option>
              <option value="balanced">均衡</option>
              <option value="aggressive">激进</option>
            </select>
            <span class="setting-label">帧率上限</span>
            <input type="number" v-model.number="config.fps_limit" step="0.5" min="1" max="10" class="input narrow" />
            <span class="text-dim text-sm">fps</span>
          </div>
          <div class="setting-row">
            <span class="setting-label">imgsz 覆盖</span>
            <input type="number" v-model.number="config.imgsz_override" placeholder="默认走 profile" class="input narrow" />
            <span class="setting-label">推理线程数</span>
            <input type="number" v-model.number="config.infer_threads" placeholder="默认自动" class="input narrow" />
          </div>
        </div>

        <div class="card mt-4">
          <div class="card-title">关于</div>
          <div class="about-grid">
            <div class="about-item"><span class="label">版本</span><span class="value">v{{ version }}</span></div>
            <div class="about-item"><span class="label">策略版本</span><span class="value">{{ policyVersion }}</span></div>
            <div class="about-item"><span class="label">许可声明</span><span class="value text-sm">内部使用，不分发</span></div>
          </div>
        </div>
      </div>

      <!-- 右列 -->
      <div class="col">
        <div class="card">
          <div class="card-title">运行</div>
          <div class="setting-row">
            <span class="setting-label">日志保留天数</span>
            <input type="number" v-model.number="config.log_retention_days" class="input narrow" />
            <span class="setting-label">日志目录</span>
            <span class="text-sm mono path">{{ config.log_dir || '默认（应用数据目录/logs）' }}</span>
          </div>
          <div class="setting-row">
            <label><input type="checkbox" v-model="config.mouse_follow" @change="save" /> 鼠标路径跟随（检测到目标时自动把光标移到该位置）</label>
          </div>
        </div>

        <div class="card mt-4">
          <div class="card-title">隐私</div>
          <div class="setting-row">
            <label><input type="checkbox" v-model="config.save_screenshots" /> 保存截图</label>
            <span class="setting-label">保留天数</span>
            <input type="number" v-model.number="config.screenshot_days" class="input narrow" />
            <label><input type="checkbox" v-model="config.blur_sensitive" /> 敏感区域模糊</label>
          </div>
          <div class="setting-row">
            <span class="setting-label">截图保存位置</span>
            <span class="text-sm mono path">{{ config.screenshot_path || '默认（软件目录\\images）' }}</span>
            <button class="text-btn" @click="chooseScreenshotDir">选择位置</button>
          </div>
          <div class="setting-row">
            <label><input type="checkbox" v-model="config.upload_structured" /> 允许上传结构化数据</label>
            <span class="text-dim text-sm">v1 默认全关，纯本地</span>
          </div>
        </div>

        <div class="card mt-4 save-bar">
          <span class="text-sm saved-hint" v-if="saved">✓ 已保存</span>
          <button class="big-btn primary" @click="save">保存设置</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref, onMounted } from 'vue'
import { useAppStore } from '@/stores/app'
import { api } from '@/api'

const app = useAppStore()

const version = ref('0.17')
const policyVersion = ref('—')
const saved = ref(false)

const config = reactive<any>({
  mode: 'observe',
  performance_tier: 'balanced',
  fps_limit: 2.5,
  imgsz_override: null as number | null,
  infer_threads: null as number | null,
  log_retention_days: 30,
  log_dir: '',
  save_screenshots: true,
  screenshot_days: 7,
  screenshot_path: '',
  mouse_follow: true,
  blur_sensitive: false,
  upload_structured: false,
})

async function load() {
  try {
    const c = await api.getConfig()
    if (c) Object.assign(config, c)
  } catch (e) {
    console.warn('[Settings] 加载配置失败:', e)
  }
  try {
    version.value = await api.getVersion()
  } catch { /* 忽略 */ }
}

async function save() {
  try {
    await api.saveConfig(JSON.parse(JSON.stringify(config)))
    saved.value = true
    window.setTimeout(() => (saved.value = false), 2000)
  } catch (e) {
    console.warn('[Settings] 保存配置失败:', e)
  }
}

function onModeChange() {
  // 切换运行模式即时生效
  save()
}

async function chooseScreenshotDir() {
  try {
    const p = await api.selectScreenshotDir()
    if (p) {
      config.screenshot_path = p
      await save()
    }
  } catch (e) {
    console.warn('[Settings] 选择目录失败:', e)
  }
}

onMounted(load)
</script>

<style scoped>
.settings-view { padding: 12px 16px; }
.settings-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  max-width: 1040px;
  margin: 0 auto;
  align-items: start;
}
.col { min-width: 0; }
.card { padding: 10px 12px; }
.mt-4 { margin-top: 12px; }
.card-title { font-size: 12px; color: var(--color-text-dim); margin-bottom: 8px; }

.mode-section { display: flex; flex-direction: column; gap: 6px; }
.mode-option { display: flex; gap: 8px; cursor: pointer; padding: 7px 10px; border: 1px solid var(--color-border); border-radius: 6px; }
.mode-option.disabled { opacity: 0.4; }
.mode-option.extreme { border-color: var(--color-red); }
.mode-option:has(input:checked) { border-color: var(--color-blue); background: rgba(10, 132, 255, 0.06); }
.mode-option.extreme:has(input:checked) { border-color: var(--color-red); background: rgba(255, 59, 48, 0.08); }
.mode-name { display: block; font-size: 13px; font-weight: 600; }
.mode-desc { display: block; margin-top: 2px; font-size: 11px; }

.setting-row { display: flex; align-items: center; gap: 8px; padding: 6px 0; border-bottom: 1px solid #1a1a2e; flex-wrap: wrap; }
.setting-row:last-child { border-bottom: none; }
.setting-label { font-size: 12px; color: var(--color-text-dim); flex-shrink: 0; }
.select, .input { background: #1a1a2e; color: var(--color-text); border: 1px solid var(--color-border); border-radius: 4px; padding: 3px 8px; font-size: 12px; }
.input.narrow { width: 70px; }
.text-btn { background: transparent; color: var(--color-blue); font-size: 12px; padding: 2px 8px; }

.about-grid { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 10px; }
.about-item { display: flex; flex-direction: column; gap: 2px; }
.about-item .label { font-size: 11px; color: var(--color-text-dim); }
.about-item .value { font-size: 13px; }

.mono { font-family: monospace; }
.path { max-width: 220px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.text-sm { font-size: 11px; }
.text-dim { color: var(--color-text-dim); }

.save-bar { display: flex; align-items: center; justify-content: flex-end; gap: 12px; padding: 10px 12px; }
.saved-hint { color: var(--color-green); font-size: 12px; }
.big-btn.primary { padding: 8px 22px; font-size: 13px; font-weight: 600; background: var(--color-blue); color: #fff; border-radius: 6px; }
</style>
