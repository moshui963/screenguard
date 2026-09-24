<template>
  <div class="status-bar" :class="app.modeColor">
    <!-- 模式指示 -->
    <div class="status-item mode-indicator" :class="app.modeColor">
      <span class="mode-dot pulse" :class="app.modeColor"></span>
      <span class="mode-label">{{ app.modeLabel }}</span>
    </div>

    <div class="status-divider"></div>

    <!-- 实时心跳 -->
    <div class="status-item">
      <span class="status-label">抓屏</span>
      <span class="status-value">{{ hb.last_capture_ago || '—' }}</span>
    </div>
    <div class="status-item">
      <span class="status-label">fps</span>
      <span class="status-value" :class="{ yellow: (hb.dropped_count || 0) > 0 }">
        {{ hb.actual_fps?.toFixed(1) || '0.0' }}
      </span>
    </div>
    <div class="status-item" v-if="(hb.dropped_count || 0) > 0">
      <span class="status-label">丢弃</span>
      <span class="status-value yellow">{{ hb.dropped_count }}</span>
    </div>

    <div class="status-divider"></div>

    <!-- 今日计数 -->
    <div class="status-item">
      <span class="status-label">已拦截</span>
      <span class="status-value green">{{ hb.today_blocked || 0 }}</span>
    </div>
    <div class="status-item">
      <span class="status-label">已上报</span>
      <span class="status-value blue">{{ hb.today_reported || 0 }}</span>
    </div>
    <div class="status-item" v-if="(hb.today_false || 0) > 0">
      <span class="status-label">误点</span>
      <span class="status-value red">{{ hb.today_false }}</span>
    </div>

    <div class="status-divider"></div>

    <!-- profile 摘要 -->
    <div class="status-item">
      <span class="status-label">imgsz</span>
      <span class="status-value">{{ hb.imgsz || '—' }}</span>
    </div>
    <div class="status-item" v-if="hb.profile_date">
      <span class="status-label">自检</span>
      <span class="status-value">{{ hb.profile_date }}</span>
    </div>

    <!-- 右侧 -->
    <div class="status-right">
      <!-- 版本号（编译期注入，每次构建自动 +0.01）-->
      <div class="status-item version-item">
        <span class="status-label">v</span>
        <span class="status-value">{{ displayVersion }}</span>
      </div>

      <div class="pause-dropdown">
        <button class="pause-btn" @click="showPauseMenu = !showPauseMenu">
          {{ app.currentMode === 'paused' ? '恢复' : '暂停' }}
        </button>
        <div class="pause-menu" v-if="showPauseMenu">
          <div class="pause-option" @click="doPause(10)">暂停 10 分钟</div>
          <div class="pause-option" @click="doPause(30)">暂停 30 分钟</div>
          <div class="pause-option" @click="doPause(60)">暂停 60 分钟</div>
          <div class="pause-option" @click="doPause(0)">直到手动恢复</div>
          <div class="pause-option" v-if="app.currentMode === 'paused'" @click="doResume">立即恢复</div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useAppStore } from '@/stores/app'
import { api } from '@/api'

const app = useAppStore()
const showPauseMenu = ref(false)

const hb = computed(() => app.heartbeat || {})

// 版本号：优先后端 getVersion，回退到心跳中的 version 字段
const version = ref('')
const displayVersion = computed(() => version.value || (hb.value as any).version || '—')
onMounted(async () => {
  try {
    version.value = await api.getVersion()
  } catch {
    // 后端未连接（如纯前端开发模式）时，依赖心跳中的 version 字段
  }
})

async function doPause(minutes: number) {
  showPauseMenu.value = false
  if (minutes === 0) {
    app.pause(0)
  } else {
    app.pause(minutes)
  }
}

async function doResume() {
  showPauseMenu.value = false
  app.resume()
}
</script>

<style scoped>
.status-bar {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 12px;
  height: 32px;
  background: #0d1117;
  border-bottom: 1px solid var(--color-border);
  flex-shrink: 0;
  font-size: 12px;
}

.status-item {
  display: flex;
  align-items: center;
  gap: 4px;
}

.status-label {
  color: var(--color-text-dim);
}

.status-value {
  font-weight: 600;
}

.green { color: var(--color-green); }
.blue { color: var(--color-blue); }
.red { color: var(--color-red); }
.yellow { color: var(--color-yellow); }
.gray { color: var(--color-gray); }
.orange { color: var(--color-orange); }

.status-divider {
  width: 1px;
  height: 16px;
  background: var(--color-border);
  margin: 0 4px;
}

.mode-indicator {
  font-weight: 700;
}

.mode-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  display: inline-block;
}

.mode-dot.red { background: var(--color-red); }
.mode-dot.blue { background: var(--color-blue); }
.mode-dot.gray { background: var(--color-gray); }
.mode-dot.orange { background: var(--color-orange); }

.status-right {
  margin-left: auto;
}

.pause-btn {
  padding: 4px 12px;
  font-size: 12px;
  background: #1a1a2e;
  border: 1px solid var(--color-border);
}

.pause-dropdown {
  position: relative;
}

.pause-menu {
  position: absolute;
  right: 0;
  top: 100%;
  background: var(--color-bg-card);
  border: 1px solid var(--color-border);
  border-radius: 6px;
  padding: 4px 0;
  z-index: 100;
  min-width: 160px;
}

.pause-option {
  padding: 8px 16px;
  cursor: pointer;
  font-size: 13px;
}

.pause-option:hover {
  background: var(--color-bg-hover);
}
</style>
