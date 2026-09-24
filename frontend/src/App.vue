<template>
  <div class="app-root" :class="{ 'auto-border': app.isAutoMode }">
    <!-- 全局状态条 PRD 3.0 -->
    <StatusBar v-if="!isOnboarding" />

    <!-- 顶部导航 -->
    <nav v-if="!isOnboarding" class="nav-bar">
      <div class="nav-left">
        <span class="app-title">屏净</span>
        <span class="app-subtitle">ScreenGuard</span>
      </div>
      <div class="nav-tabs">
        <RouterLink to="/" class="nav-tab" active-class="active">监控台</RouterLink>
        <RouterLink v-if="app.isMaintainer" to="/inspector" class="nav-tab" active-class="active">调试台</RouterLink>
        <RouterLink to="/settings" class="nav-tab" active-class="active">设置</RouterLink>
      </div>
    </nav>

    <!-- 主内容区 -->
    <main class="main-content">
      <RouterView />
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { useAppStore } from '@/stores/app'
import StatusBar from '@/components/StatusBar.vue'

const app = useAppStore()
const route = useRoute()

const isOnboarding = computed(() => route.name === 'onboarding')

let heartbeatTimer: number | undefined

onMounted(() => {
  app.fetchHeartbeat()
  // 1000ms 节流刷新心跳；不自动跳转授权页，避免授权/重启死循环
  heartbeatTimer = window.setInterval(() => {
    app.fetchHeartbeat()
  }, 1000)
})

onUnmounted(() => {
  if (heartbeatTimer) clearInterval(heartbeatTimer)
})
</script>

<style scoped>
.app-root {
  display: flex;
  flex-direction: column;
  height: 100vh;
  overflow: hidden;
}

.nav-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
  height: 40px;
  background: #111;
  border-bottom: 1px solid var(--color-border);
  flex-shrink: 0;
}

.nav-left {
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.app-title {
  font-size: 16px;
  font-weight: 700;
  color: #fff;
}

.app-subtitle {
  font-size: 11px;
  color: var(--color-text-dim);
}

.nav-tabs {
  display: flex;
  gap: 2px;
}

.nav-tab {
  padding: 6px 14px;
  border-radius: 6px;
  font-size: 13px;
  color: var(--color-text-dim);
  transition: all 0.15s;
}

.nav-tab:hover {
  color: #fff;
  background: rgba(255, 255, 255, 0.05);
}

.nav-tab.active {
  color: #fff;
  background: var(--color-bg-hover);
}

.main-content {
  flex: 1;
  overflow: hidden;
  position: relative;
}
</style>
