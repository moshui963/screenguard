import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

const routes: RouteRecordRaw[] = [
  {
    path: '/onboarding',
    name: 'onboarding',
    component: () => import('@/views/OnboardingView.vue'),
    meta: { title: '首次运行引导', fullScreen: true },
  },
  {
    path: '/',
    name: 'dashboard',
    component: () => import('@/views/DashboardView.vue'),
    meta: { title: '监控台' },
  },
  {
    path: '/inspector',
    name: 'inspector',
    component: () => import('@/views/InspectorView.vue'),
    meta: { title: '调试台', maintainerOnly: true },
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('@/views/SettingsView.vue'),
    meta: { title: '设置' },
  },
  // 兜底：未知路径（含已移除的策略中心/自检/复盘/统计）一律回监控台
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach((to, from, next) => {
  document.title = `${to.meta.title || '屏净'} — ScreenGuard`
  // TODO: 维护者权限检查 — maintainerOnly 路由对普通使用者不渲染
  next()
})

export default router
