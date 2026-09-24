// ============================================================
// API 层 — 与 Go 后端通信
// Wails v3 通过 window.wails.Call.ByName 调用 Go 方法
// 开发时走 HTTP proxy
// ============================================================

const isDev = import.meta.env.DEV
const servicePrefix = 'main.ScreenGuardService'

// Wails runtime 注入
declare global {
  interface Window {
    go?: any
    wails?: any
  }
}

function toServiceMethod(method: string): string {
  return method.startsWith('App.') ? `${servicePrefix}.${method.slice(4)}` : method
}

export async function call<T = any>(method: string, ...args: any[]): Promise<T> {
  if (isDev) {
    // 开发模式走 HTTP
    const res = await fetch(`/api/${method}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(args),
    })
    if (!res.ok) throw new Error(`API ${method} failed: ${res.statusText}`)
    return res.json()
  }

  // Wails v3 runtime
  const byName = window.wails?.Call?.ByName
  if (byName) {
    return byName(toServiceMethod(method), ...args)
  }

  // 兼容旧式 window.go 注入
  if (window.go) {
    const parts = method.split('.')
    let obj = window.go
    for (const p of parts.slice(0, -1)) {
      obj = obj[p]
    }
    return obj[parts[parts.length - 1]](...args)
  }
  throw new Error('Wails runtime not available')
}

// ---- Heartbeat ----
export const api = {
  getHeartbeat: () => call<Heartbeat>('App.GetHeartbeat'),
  getPermissions: () => call<PermissionStatus>('App.GetPermissions'),
  requestScreenCapturePermission: () => call<boolean>('App.RequestScreenCapturePermission'),
  requestAccessibilityPermission: () => call<boolean>('App.RequestAccessibilityPermission'),
  setMode: (mode: string) => call('App.SetMode', mode),
  pause: (minutes: number) => call('App.Pause', minutes),
  resume: () => call('App.Resume'),
  runSelfTest: () => call('App.RunSelfTest'),
  quitApp: () => call('App.QuitApp'),
  restartApp: () => call('App.RestartApp'),
  getEvents: (limit: number, offset: number, filter?: string) =>
    call('App.GetEvents', limit, offset, filter),
  getEvent: (id: number) => call('App.GetEvent', id),
  getPolicy: () => call('App.GetPolicy'),
  savePolicy: (yaml: string, note: string) => call('App.SavePolicy', yaml, note),

  // ---- 逐类策略（调试台"类别控制"）----
  getCategoryPolicy: () => call<Record<string, { conf: number; mode: string }>>('App.GetCategoryPolicy'),
  saveCategoryPolicy: (updates: Record<string, { conf: number; mode: string }>) =>
    call('App.SaveCategoryPolicy', updates),

  // ---- 应用配置（设置页）----
  getConfig: () => call<Record<string, any>>('App.GetConfig'),
  saveConfig: (cfg: Record<string, any>) => call('App.SaveConfig', cfg),
  selectScreenshotDir: () => call<string>('App.SelectScreenshotDir'),
  simulateReplay: (from: string, to: string, yaml: string) =>
    call('App.SimulateReplay', from, to, yaml),
  getBlacklist: () => call('App.GetBlacklist'),
  addBlacklist: (type: string, value: string, reason: string) =>
    call('App.AddBlacklist', type, value, reason),
  getModelVersions: () => call('App.GetModelVersions'),
  switchModel: (id: number) => call('App.SwitchModel', id),
  getReviewQueue: (limit: number) => call('App.GetReviewQueue', limit),
  markNotPopup: (eventId: number) => call('App.MarkNotPopup', eventId),
  exportDataset: (ids: number[]) => call('App.ExportDataset', ids),
  getStats: (days: number) => call('App.GetStats', days),
  feedImage: (path: string) => call('App.FeedImage', path),

  // ---- 调试台画布帧推送 ----
  getLiveFrame: () => call<FramePayload | null>('App.GetLiveFrame'),
  setLiveEnabled: (v: boolean) => call('App.SetLiveEnabled', v),
  freezeFrame: (v: boolean) => call('App.FreezeFrame', v),
  getFrameStats: () => call<FrameStats>('App.GetFrameStats'),
  getVersion: () => call<string>('App.Version'),
}

/**
 * 订阅后端帧推送。
 * Wails v3 注入的运行时提供 window.wails.Events.On，回调收到 WailsEvent，数据在 .data。
 * 返回取消订阅函数；运行时不可用时返回 no-op（调用方应回退到轮询 getLiveFrame）。
 */
export function onWailsFrame(cb: (p: FramePayload) => void): () => void {
  const ev = (window as any).wails?.Events
  if (!ev?.On) return () => {}
  const off = ev.On('sg:frame', (e: any) => {
    const payload = e?.data !== undefined ? e.data : e
    if (payload) cb(payload as FramePayload)
  })
  return () => {
    try {
      if (typeof off === 'function') off()
      else if (ev.Off) ev.Off('sg:frame', cb)
    } catch { /* 忽略解绑失败 */ }
  }
}

export function hasWailsEvents(): boolean {
  return Boolean((window as any).wails?.Events?.On)
}

// ---- 帧推送数据类型（与 internal/app/framepush.go 的 json tag 对齐）----
export interface FrameBox {
  cls: number
  cls_name: string
  conf: number
  role: string
  box: [number, number, number, number]
  center: [number, number]
  box_model: [number, number, number, number]
  gate_passed: boolean
  reject_reason?: string
}

export interface RoiPayload {
  x: number
  y: number
  w: number
  h: number
  imgsz2: number
  jpeg?: string
}

export interface FramePayload {
  screen_w: number
  screen_h: number
  img_w: number
  img_h: number
  scale: number
  jpeg: string
  imgsz: number
  ratio: number
  dw: number
  dh: number
  boxes: FrameBox[]
  frame_index: number
  capture_ms: number
  model_ms: number
  total_ms: number
  action?: string
  result?: string
  roi?: RoiPayload
  has_popup: boolean
  frozen: boolean
  ts: number
}

export interface FrameStats {
  pushed: number
  skipped: number
  enabled: boolean
  frozen: boolean
}

export interface Heartbeat {
  mode: string
  last_capture_ago: string
  actual_fps: number
  dropped_count: number
  today_blocked: number
  today_reported: number
  today_false: number
  imgsz: number
  profile_date: string
  screen_authorized: boolean
  accessibility_authorized: boolean
}

export interface PermissionStatus {
  screen: boolean
  accessibility: boolean
  screen_state?: 'unauthorized' | 'needs_restart' | 'ok'
}
