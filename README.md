<div align="center">

# 🛡️ ScreenGuard · 屏净

**Automatically close annoying desktop popups on Windows — 100% local AI, zero cloud, privacy-first.**

自动识别并关闭桌面弹窗的 Windows 桌面端工具 · 全程本地推理 · 画面绝不离开你的电脑

<p>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License: MIT"></a>
  <img src="https://img.shields.io/badge/platform-Windows%2010%2B-0078D6.svg" alt="Platform: Windows 10+">
  <img src="https://img.shields.io/badge/version-v0.34-brightgreen.svg" alt="Version: v0.34">
  <img src="https://img.shields.io/github/v/release/moshui963/screenguard?label=release&color=success" alt="Latest release">
  <img src="https://img.shields.io/github/downloads/moshui963/screenguard/total?label=downloads&color=informational" alt="Total downloads">
  <img src="https://img.shields.io/badge/Go-1.21%2B-00ADD8.svg" alt="Go: 1.21+">
  <img src="https://img.shields.io/badge/build-CGO__DISABLED-9f9f9f.svg" alt="Pure Go build">
</p>

</div>

---

## ✨ Features · 功能特性

- 🤖 **Local AI detection** — A YOLOv26 end-to-end model runs entirely on your PC via ONNX Runtime. No internet, no cloud, no privacy leaks.
  本地 AI 识别：YOLOv26 端到端模型在你的电脑上用 ONNX Runtime 跑，不联网、不上云、不泄露画面。
- ⚡ **9-step safety decision chain** — forbidden zones (fullscreen / RDP / lock screen / low battery / paused), user-idle guard, per-class policy, 4-frame consistency, click budget… it only clicks when it is *truly* safe.
  9 步安全决策链：禁区 / 用户活跃 / 策略门禁 / 容器约束 / 几何 / 阈值 / 帧一致性 / 点击预算，只有在「确实安全」时才点。
- 🚀 **Burst mode** — when a popup appears it stops skipping static frames and hunts the ✕ at up to 8 fps, so close-buttons get clicked in ~1s instead of 5–7s.
  突发模式：弹窗出现即提速搜捕关闭按钮，点击延迟从 5–7s 降到约 1s。
- 🛡️ **Self-click protection** — before every click it enumerates its own window rectangles, so it never clicks its own close button.
  自激防护：点击前枚举自身窗口矩形，绝不会点到自己的关闭钮。
- 🌐 **Extreme mode** — ignore the "are you using the PC?" hesitation and click immediately (for kiosks / automation).
  极限模式：忽略「你正在操作」的犹豫，发现即点（适合无人值守 / 自动化）。
- 📊 **Full audit trail** — every decision is written to an immutable JSONL event stream + SQLite; replay & review anytime.
  完整审计：每次决策写入不可变 JSONL 事件流 + SQLite，随时复盘。
- 🔌 **Hot-reload policy** — YAML policies reload without restart.
  策略热加载：改 `policy.yaml` 无需重启。
- 🪶 **Pure Go, no C compiler** — ONNX Runtime is loaded at runtime via `syscall`; builds with `CGO_ENABLED=0`.
  纯 Go 构建：运行时动态加载 `onnxruntime.dll`，无需任何 C 编译器。

---

## 🚀 Quick Start · 快速开始

### Option A — Download prebuilt（推荐 / 最简单）
前往 **[Releases](https://github.com/moshui963/screenguard/releases)**，下载最新 `screenguard-windows-amd64-vX.Y.Z.zip`，解压后双击 `screenguard.exe` 即可。

> 开箱即用包已含 `onnxruntime.dll` 与 `yolo26m.onnx`，无需额外下载模型或运行时。
发布包已内含 `onnxruntime.dll` + `yolo26m.onnx` + 配置，开箱即用。

### Option B — Build from source
```bash
# 1. 克隆
git clone https://github.com/moshu963/screenguard.git
cd screenguard

# 2. 拉取 ONNX Runtime（一次性，约 60MB）
powershell -ExecutionPolicy Bypass -File scripts/fetch_deps.ps1

# 3. 放入模型：把你的 yolo26m.onnx 放到  yolo模型/yolo26m.onnx
#    （或直接从 Releases 下载模型文件）

# 4. 构建（纯 Go，无需 C 编译器）
powershell -ExecutionPolicy Bypass -File scripts/build-win.ps1

# 5. 运行
build/windows/screenguard.exe
```

> 💡 版本号约定：每次编译本体（screenguard.exe）版本号自动 +0.01，真值在 `version.txt`，
> 经 `-ldflags -X screenguard/internal/version.Version` 注入，启动日志与托盘会显示当前版本。

---

## 🧠 How it works · 工作原理

```
抓屏 → 两级视觉推理（找容器 → 找出口 ✕）→ 安全策略判定 → 自动点击 → 消失验证
     → 事件日志落盘 → 复盘队列 → 标注回流 → 模型 / 策略迭代
```

**9-step decision priority chain（顺序不可变 / order is fixed）：**

| # | Step | Skip condition |
|---|------|----------------|
| 1 | 禁区检查 Forbidden zone | 全屏 / 锁屏 / 投屏 / 远程桌面 / 低电 / 已暂停 |
| 2 | 用户空闲检测 User-idle | 距上次输入 < 1.5s → 不点 |
| 3 | profile 门禁 Profile gate | 该类在本机 mode ≠ click → 不点 |
| 4 | 容器约束 Container | 出口中心不在容器框内 → blocked |
| 5 | 几何合法性 Geometry | 面积 / 重叠率不合法 |
| 6 | 逐类阈值 Threshold | conf < 阈值 → low_conf |
| 7 | 4 帧一致性 Consistency | 未凑齐或 std ≥ 1px → 等待 |
| 8 | 点击预算 Budget | 同容器 2s 内已点 / 全局 > 60 次/时 |
| 9 | 执行点击 Execute | → VERIFYING |

---

## 🏗️ Architecture · 技术架构

| 层 Layer | 选型 Choice |
|---|---|
| 壳 Shell | Go 1.21+ + Wails v3 |
| 前端 Frontend | Vue 3 + TypeScript + Vite + Pinia |
| 推理 Inference | ONNX Runtime（**纯 Go 运行时动态加载，无 cgo**） |
| 模型 Model | YOLOv26 端到端（NMS-free） |
| 抓屏 Capture | Windows GDI BitBlt（Win10+） |
| 存储 Storage | SQLite + JSONL 双写 |

---

## ⚙️ Configuration · 配置

见 `configs/config.json` 与 `configs/policy.yaml`。常用字段：

| 字段 | 说明 |
|------|------|
| `mode` | `observe`（仅观察不点）/ `auto`（智能）/ `extreme`（极限，发现即点） |
| `fps_limit` | 抓屏帧率上限（默认 2.5） |
| `model_path` | 模型路径，默认 `models/yolo26m.onnx` |
| `policy_path` | 策略文件路径 |
| `save_screenshots` | 是否保存抓屏用于复盘 |

---

## 📁 Project Structure · 目录结构

```
screenguard/
├── cmd/screenguard/      # 主进程入口
├── internal/
│   ├── app/              # 应用主控：帧调度、推理编排、事件落盘
│   ├── capture/          # 抓屏抽象 + 帧差门控
│   ├── click/            # 点击执行 + 自激防护 + 消失验证
│   ├── engine/           # 决策引擎：9 步优先级链 + 4 帧一致性 + 点击预算
│   ├── events/           # JSONL 事件流（不可变审计原始流）
│   ├── infer/            # ONNX 推理（纯 Go 动态加载）+ 两级调度 + 坐标逆变换
│   ├── policy/           # 策略引擎：YAML 解析 + 热加载
│   ├── selftest/         # 自检系统
│   ├── storage/          # SQLite 表结构 + CRUD
│   ├── types/            # 全局类型 + 枚举常量
│   └── platform/         # 平台抽象：Windows 抓屏 + 托盘 + 叠加层 + 自激检测
├── frontend/src/         # Vue 3 前端（监控台 / 调试台 / 设置 / 引导）
├── configs/              # config.json + policy.yaml
├── scripts/              # build-win.ps1 / fetch_deps.ps1 / gen_ort_table.py
└── build/               # 图标与托盘资源（appicon.ico / tray_*.png）
```

> ℹ️ `build/windows/`、`third_party/`、`yolo模型/`、`*.onnx`、`*.dll` 等二进制与运行时数据均已在 `.gitignore` 中排除，
> 请通过 `scripts/fetch_deps.ps1` 与 Releases 获取。

---

## 🛣️ Roadmap · 路线图

- [ ] macOS / Linux 抓屏后端
- [ ] 插件式模型市场
- [ ] 智能「白名单」学习（少误杀）
- [ ] 多显示器 / 高 DPI 精细化支持

---

## 🤝 Contributing · 贡献

PR 与 Issue 都欢迎！提交前请保证：

```bash
go vet ./...
go test ./...
```

重大改动建议先开 Issue 讨论。

---

## 📄 License · 许可

[MIT](LICENSE) © 2026 moshu963

> 本软件按「现状」提供，不保证适用于任何特定用途。自动点击存在误点风险，
> 请在理解决策链与安全边界的前提下使用，重要操作前建议先用 `observe` 模式观察。
