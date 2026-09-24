# 屏净（ScreenGuard）产品需求文档 PRD v0.1

**应用名称**：屏净 / ScreenGuard（工作代号 `tanchuang`，正式名称待定）

---

## 版本说明

本文档为合并版，整合了历史版本与最新版全部有效内容，以当前代码库实际功能为准。

本文档对应 **v1（macOS 优先）** 范围。v1 已产出的算法资产与工具链如下，PRD 中的功能均以这些已验证能力为前提：

| 已有资产 | 说明 |
|---|---|
| `tangchuang/yolo26m.pt` | 已训练的检测权重，10 类，训练 imgsz=1280，21.79M 参数，端到端 NMS-free |
| `tangchuang/redetr_v2.onnx` | RT-DETR 备选模型，输入静态锁死 1024×1024，**v1 不采用**（纯 CPU 场景 622ms/帧且无法降档） |
| `popup_infer.py` | 算法基准实现：两级调度、逐类阈值、几何去重、容器约束、坐标换算 |
| `popup_eval.py` | 业务指标评估台：点击命中率、中心偏差分布、每千张负帧误检数 |
| `bench_popup_models.py` | 模型规格与 CPU 耗时基准 |
| `实测记录_M4_20260831.md` | 实测数据与已知风险清单 |

**关键前提假设**（如不成立需回改本文档）：

1. v1 仅公司内部使用，不分发、不对外，因此 Ultralytics AGPL-3.0 不触发开源义务。
2. v1 不做账号体系，单机单用户，所有配置在本地。
3. 感知决策 100% 依赖视觉，不引入 Accessibility / UIA / 窗口枚举参与"是不是弹窗"的判断（抓屏与排除自身窗口除外，见 4.3.7）。
4. 策略更新通过共享盘或内网 HTTP 静态文件下发，v1 不自建服务端。

---

## 1. 应用概述

### 1.1 应用名称

屏净 / ScreenGuard

### 1.2 应用描述

**一句话定位**：一款常驻桌面后台的 macOS / Windows 客户端工具，用视觉模型实时识别屏幕上的弹窗及其关闭入口，并在满足安全条件时自动关闭它。

**核心价值**：解决了"办公电脑上各种推广弹窗、开机自启提示、活动弹层反复出现，手动关闭打断工作、且无法根治"的问题。用户不需要知道它存在，屏幕就干净了。

**核心功能闭环**：

```
抓屏 → 两级视觉推理（找容器 → 找出口）→ 安全策略判定 → 自动点击 → 消失验证
     → 事件日志落盘 → 复核队列 → 标注回流 → 模型/策略迭代
```

**特色功能**：

1. **容器约束作为唯一主闸**。实测证明模型无法凭外观区分"弹窗的 ×"和"正常窗口标题栏的 ×"（同类别、置信度仅差 0.26），因此"出口中心必须落在弹窗容器框内"是放行的硬性前置条件，而非可选优化。
2. **合成探针自检 + per-device profile**。软件自己画一张真值 100% 已知的假弹窗，走完整生产链路跑一遍，自动得出这台机器该用多大 imgsz、哪些类别允许自动点，用户零输入。
3. **每一步可拒、每件事可查**。四帧一致性门控 + 点击后消失验证 + 全量原始框落盘，任何不确定都倒向"不点"，且能事后完整复现"当时为什么会点/为什么不点"。
4. **策略热更新 + 离线重放**。改阈值不重装；replay 能回答"如果按新策略，过去 100 小时会点几次、错几次"。
5. **数据飞轮闭环内建**。复核队列一键标注 → 导出 YOLO 格式数据集 → 直接投喂训练。

---

## 2. 用户与使用场景

### 2.1 目标用户

本产品为单机桌面工具，无登录体系，"角色"实际是本机内的**能力边界**而非账号：

| 角色 | 身份 + 核心诉求 + 权限边界 |
|---|---|
| **普通使用者**（同事） | 被弹窗骚扰的办公人员。诉求：别烦我、别误关我的东西。权限：只能看到监控台与统计，只能"暂停/恢复"和"标记误报"，不能改阈值、不能切模型、不能关自检。 |
| **维护者**（你） | 工具的开发与运营者。诉求：定位误点原因、调策略、迭代模型。权限：全部功能，含策略编辑、模型切换、数据集导出、日志查询。 |
| **策略下发方**（v1 由维护者兼任） | 维护共享盘上的 `policy.yaml`。诉求：不发版就能处理新出现的弹窗样式。权限：只写策略文件，不接触任何用户机器数据。 |
| **审计/安全合规**（只读） | 关心"你是否在静默截图、数据去哪了"。诉求：可核对留存范围。权限：读设置页的隐私声明与留存策略，读日志目录，不操作。 |

角色识别方式：v1 通过配置文件 `maintainer: true` + macOS 用户组判断，不做登录。

### 2.2 核心使用场景

**普通使用者**

1. 同事首次安装 → 按引导授予屏幕录制与辅助功能权限 → 自检通过 → 进入观察模式，工具开始工作。
2. 同事在演示/共享屏幕 → 按全局快捷键暂停 10 分钟 → 期间不检测不点击 → 到时自动恢复。
3. 工具误关了一个正常窗口 → 点击托盘"刚才那个不该点" → 该应用特征进黑名单 → 后续不再介入。
4. 同事打开监控台 → 看到"本周已为你关闭 63 个弹窗" → 建立信任，继续留存。

**维护者**

5. 维护者收到"某软件弹窗没关掉"的反馈 → 打开调试台 → 冻结帧 → 双画布对照发现容器被识别但出口在模型输入里只有 5px → 调整该设备 imgsz 档位 → 生效。
6. 维护者要评估新模型 → 模型管理里并存两个版本 → 用 `popup_eval.py` 在同一验证集上比点击命中率与每千张负帧误检数 → 切换启用。
7. 维护者改了某类阈值 → 策略中心点"模拟" → 系统基于历史日志重放，输出"新增 12 次点击、其中 2 次落在黑名单应用" → 决定是否发布。
8. 维护者排查一次线上误点 → 复盘中心按时间找到该事件 → 详情抽屉显示完整决策链、当时的策略版本号、原始框列表和该帧截图 → 定位到是 4 帧门控被动画期抖动绕过。
9. 维护者攒够 200 条待标注帧 → 内置标注修正 → 导出 YOLO zip → 上传 AutoDL 重训。
10. 维护者要给全组下发"某新弹窗放行" → 更新共享盘 `policy.yaml` → 客户端定时拉取 + 版本号比对 → 2 秒内热生效，不重装。

**边界场景**

11. 新同事的机器是 5K 外接屏 → 自检发现任何 imgsz 档位下偏差都不达标 → 全部类别降级为只上报，界面明确提示"本机视觉条件不足" → 不产生任何误点。
12. 电脑从睡眠唤醒 / 插拔显示器 → profile 指纹变化 → 自动重跑自检 → 期间不点击。

---

## 3. 页面结构与功能说明

### 页面层级结构

```
屏净 ScreenGuard（桌面客户端，单窗口 + 托盘常驻）
├── 首次运行引导 Onboarding（仅未授权/未自检时出现）
│   ├── 权限引导页（屏幕录制 / 辅助功能 / 登录项）
│   ├── 自检执行页（探针运行中）
│   └── 自检结果页（profile 可视化 + 放行结论）
├── 主窗口（顶部标签切换，关闭即隐藏到托盘）
│   ├── 监控台 Dashboard（默认视图，面向普通使用者）
│   ├── 调试台 Inspector（双画布 + 事件流 + 逐类控制，面向维护者）
│   ├── 策略中心 Policy
│   │   ├── profile 页
│   │   ├── policy.yaml 编辑页（含 diff 预览与模拟重放）
│   │   └── 黑名单页
│   ├── 自检中心 SelfTest（重新运行、历史结果对比）
│   ├── 复盘中心 Review
│   │   ├── 复核队列
│   │   ├── 内置标注编辑器
│   │   └── 数据集导出
│   ├── 统计 Stats
│   └── 设置 Settings（模式 / 性能 / 隐私 / 日志 / 关于）
├── 全局状态条（所有视图常驻）
├── 托盘菜单 + 全局快捷键（窗口不在前台时的唯一入口）
└── 桌面叠加层 Overlay（非窗口，可选功能，受 4.3.7 自激规则约束）
```

### 3.0 全局状态条

**3.0.1 页面职责**：让任何人在任何时刻一眼看清"这个软件现在在干什么、能不能信任它"。

**3.0.2 核心功能清单**

- **模式指示**：`未授权` / `自检未通过` / `观察模式` / `自动模式` / `已暂停` 五态，配色区分。自动模式下整个窗口加一条持续可见的红色边条（安全设计，见 4.3.1）。
- **实时心跳**：最近一次抓屏时间（"0.4s 前"）、当前实际 fps、队列丢弃计数。丢弃计数 > 0 时标黄，说明这台机器跑不动。
- **今日计数**：已拦截 / 已上报 / 已误点 三个数字。
- **profile 摘要**：当前 imgsz 档位、自检日期。
- **暂停按钮**：一键暂停，下拉选 10/30/60 分钟或"直到手动恢复"。
- **数据来源**：Go 侧运行时状态，通过 Wails 事件推送，前端 500ms 节流刷新。
- **边界条件**：窗口关闭后状态条消失，等价信息由托盘 tooltip 与托盘图标颜色承载。

**3.0.3 权限说明**：所有角色可见；仅维护者可切换模式，普通使用者只能"暂停"。

### 3.1 首次运行引导 Onboarding

**3.1.1 页面职责**：把 macOS 三个必须手工授权的环节做成可完成、可校验、可自愈的闭环。

**3.1.2 核心功能清单**

- **权限卡（三项红绿灯）**：屏幕录制、辅助功能（模拟输入）、登录项。每项显示状态、用途说明、"去系统设置"按钮（直接跳到对应面板 URL Scheme）。
- **授权后重启提示**：macOS 屏幕录制权限对已运行进程不生效。检测到"已授权但当前进程无权限"时，必须显式提示"需要重启应用"，并提供"一键重启"（自杀 + 由 launchd/登录项拉起）。**这是头号支持工单，不能只写在文档里。**
- **抓屏能力实测**：点"测试抓屏"，实际截一帧并显示缩略图。全黑/仅壁纸 → 明确报"未获得权限"，而不是静默返回黑图。
- **自检执行页**：进度条 + 逐档 imgsz 的实时结果。运行前提示"屏幕会短暂出现测试窗口，约 3 秒"。
- **自检结果页**：屏幕示意图 + 四角/中心探针点 + **放大 N 倍的偏差箭头**；结论区显示推荐 imgsz、允许自动点的类别、被降级的类别及原因。
- **失败降级**：任何档位不达标 → 不报错退出，而是全部类别设为 `report`，界面写明原因，并保留"仍要强制开启"的维护者选项（二次确认 + 日志留痕）。
- **可跳过**：普通使用者可跳过自检先用观察模式，但自动模式必须等自检通过。

**3.1.3 操作流程**：启动 → 授屏幕录制 → 重启 → 授辅助功能 → 跑自检 → 看结果 → 进入监控台（观察模式）。

### 3.2 监控台 Dashboard

**3.2.1 页面职责**：面向普通使用者的唯一视图，回答"它活着吗、帮了我多少、有没有乱来"。

**3.2.2 核心功能清单**

- **"活着"的证据**：最近一帧的缩略图（含叠加框）+ 采集心跳动画 + 当前 fps。**默认视图绝不能是空白**——空白会让人以为软件坏了。
- **本周成效**：弹窗总数、自动关闭数、"相当于帮你少点了 N 次"。
- **按应用 Top 5**：哪些应用在弹（这是用户最感兴趣的信息，也是留存论据）。
- **误点记录**：最近 3 条 `failed` / 被标误报的事件，可展开详情。
- **待复核红点**：复核队列有待办时提示，普通使用者点进去只能"标记这不是弹窗"。
- **两个大按钮**：暂停 / 打开详情（跳调试台，普通使用者隐藏）。
- **边界条件**：无数据时显示"本周还没有弹窗"，不显示空表格。

**3.2.3 权限说明**：普通使用者默认视图；维护者可见全部但通常不在此停留。

### 3.3 调试台 Inspector

**3.3.1 页面职责**：维护者的主战场，把"屏幕 → 模型输入 → 决策 → 动作"整条链路可视化到可定位问题。

**3.3.2 核心功能清单**

- **双画布对照（核心）**：
  - 左：**原屏画面**，带全部叠加框。
  - 右：**模型输入**，即 letterbox 之后真正喂进张量的那张图（含黑边）。
  - 并排的价值：一眼看出"× 在模型里只剩 5px"、"弹窗被压进黑边"、"pad 位置不对"这类问题。
  - 两画布坐标联动：鼠标悬停左图画像点，右图同步高亮对应位置并显示换算后的模型空间像素尺寸。
- **分层叠加开关**：容器框 / 出口框 / **被拒绝的框**（不同颜色 + 原因标签 `outside_container`、`low_conf`、`blocked_dup`）/ 点击点十字 / 4 帧轨迹点。被拒绝的框必须能显示——"这里有个框但我没点"是软件价值的直接证据。
- **冻结帧**：暂停采集但保留当前帧与全部标注。弹窗一闪而过，没有这个就永远看不到。
- **ROI 裁切预览**：显示第二级实际吃进去的那块小图及其尺寸、外扩比例。
- **手动喂图**：拖入单张图片或选目录 → 走同一链路 → 结果进右侧表格。用于离线验证与对拍 Python 基准。
- **结构化事件流**（页面主角，不是截图那种滚动文本框）：时间 / 类别 / conf / 动作 / 结果 / 耗时；筛选器支持"只显示真的点了的"（最高频单一筛选）、按类别、按结果、按时间窗。
- **详情抽屉**：点任一条事件 → 右侧展开完整 JSON、该帧截图、原始框列表（含被删框及删除者）、生效的策略版本号、4 帧的逐帧中心与 std。
- **逐类控制表**：每类一行 = 阈值滑杆 + 模式下拉（`自动点 / 只上报 / 禁用`）+ 该类累计点击数与失败数。**这是整个软件真正的控制面，视觉权重最高。** 改完点"应用"走策略热加载。
- **单类试跑**：在当前帧上只跑指定类别，看它到底检出了什么。
- **模型管理**：多版本并存列表（文件名、sha256、训练 imgsz、类别表、评估指标、启用状态）→ 一键切换 / 回滚。显示当前跑的是 `.pt` 还是 `.onnx`、是否量化。
- **虚拟列表 + 批量刷新（工程硬约束）**：事件流必须用虚拟滚动，前端 100ms 合并一次批量更新；预览帧走 canvas 直绘，**不进 Vue 响应式系统**。否则高频帧会把 UI 拖死。

**3.3.3 操作流程**：看到可疑点击 → 冻结帧 → 双画布对照定位是尺度问题还是模型问题 → 逐类控制表调阈值 → 应用 → 观察后续事件验证。

**3.3.4 权限说明**：仅维护者可见可操作；普通使用者访问时整个标签隐藏（不是禁用置灰）。

### 3.4 策略中心 Policy

**3.4.1 页面职责**：把"能不能点"的规则集中管理，且支持不发版更新。

**3.4.2 核心功能清单**

- **三个子页**：`profile`（本机自检产物，只读 + 可手工覆盖）、`policy`（放行表）、`blacklist`（拉黑记录）。
- **policy.yaml 编辑**：CodeMirror 6（不用 Monaco，体积考虑）+ schema 校验 + 保存前 diff 预览 + 变更说明填写（自动生成策略版本号）。
- **模拟重放（高价值）**：选时间窗 → 用编辑中的新策略跑历史日志 → 输出"会点 N 次 / 新增 M 次 / 减少 K 次 / 其中 X 次落在黑名单应用"。**不发版、不等真实弹窗复现就能评估策略变更。**
- **黑名单管理**：特征类型（应用 bundle id / 窗口标题正则 / 容器尺寸区间）、原因、TTL、来源（自动 failed 3 次 / 人工标注）。到期自动解除并记日志。
- **共享盘策略拉取**：定时（默认 10 分钟）比对版本号 → 拉取 → 校验 → 热加载；拉取失败继续使用本地缓存并记 warn。
- **禁区清单可视化**：安装向导 / UAC 与安全提示 / 远程桌面窗口 / 全屏应用 / 锁屏屏保 / 电池低电，每项一个开关，**默认全开且普通使用者不可关**。

**3.4.3 操作流程**：新弹窗样式出现 → 同事反馈 → 维护者在调试台找到该帧 → 策略中心加规则 → 模拟重放确认 → 发布到共享盘 → 全员热生效。

**3.4.4 权限说明**：编辑与发布仅维护者；普通使用者只读 profile。

### 3.5 自检中心 SelfTest

**3.5.1 页面职责**：随时可重跑的"这台机器行不行"体检，兼作软件改动的回归测试。

**3.5.2 核心功能清单**

- **运行自检**：手动触发，进度与逐档结果实时回显。
- **结果对比**：与上一次自检并排 diff（imgsz 变化、偏差变化、耗时变化），用于判断"换了模型/改了配置后有没有变差"。
- **探针点可视化**：屏幕示意图 + 各点偏差箭头 + 每点数值。
- **capture_ms 硬门禁**：抓屏耗时超过基线 3 倍 → 判定本机不适合，直接建议禁用自动模式（而不是降精度硬跑）。
- **权限复检**：四项能力（抓屏 / 输入 / 登录项 / 全屏可抓）的当前状态。
- **历史留档**：每次自检结果落 `device_profile` 表，可回溯。

### 3.6 复盘中心 Review

**3.6.1 页面职责**：把日志变成训练数据，是数据飞轮在界面上的唯一接口。

**3.6.2 核心功能清单**

- **复核队列**：所有 `blocked_*`、`low_conf`、`failed`、用户标过误报的事件，带缩略图，逐条处理。
- **内置轻量标注编辑器**：在截图上拖框、改类别、删框、调框。**不做完整标注工具**，只要能修正模型的错误输出。
- **一键入集**："这帧加入待标注" → 进 `dataset_item`，状态机 `raw → labeled → exported`。
- **数据集导出**：勾选条目 → 导出 YOLO 格式 zip（images/ + labels/ + classes.txt + 生成时间 + 来源策略版本），可直接上传 AutoDL 训练。
- **负帧采集开关**：按设定间隔静默保存"无任何候选目标"的帧 → 自动补负样本（零标注成本，见 4.3.8）。
- **边界条件**：截图被隐私开关关闭时，队列只显示结构化数据，标注功能置灰并说明原因。

**3.6.3 操作流程**：队列逐条判"是弹窗/不是弹窗/出口点错了" → 需要时改框 → 累积到批量阈值 → 导出 zip → 重训。

### 3.7 统计 Stats

**3.7.1 页面职责**：量化产品价值与运行健康度。

**3.7.2 核心功能清单**

- **成效**：弹窗总数、自动关闭数、成功率、按应用 Top N、按类别分布。
- **质量**：误点率（每千小时）、每千张负帧误检数、各类点击命中率、中心偏差中位/P95。**这些是放行自动模式的门禁指标，不是装饰。**
- **性能**：单帧耗时分布、实际 fps、队列丢弃率、CPU/内存曲线、崩溃重启次数。
- **趋势**：同一 exe/bundle 的弹窗次数随时间衰减曲线——**"源头抑制成功率"是这个产品真正的价值证明**。
- 图表库用 ECharts 按需引入，不打包全量。

### 3.8 设置 Settings

**3.8.1 页面职责**：运行参数与隐私声明。

**3.8.2 核心功能清单**

- **运行模式**：观察 / 自动（自动需二次确认 + 红边条常驻 + 显示上次误点时间）。
- **性能档位**：省电 / 均衡 / 激进（映射到帧率上限与线程数）。
- **帧率上限**、**imgsz 覆盖**（默认走 profile）、**推理线程数**（默认物理核 50~70%，留余量给用户）。
- **开机自启**、**日志保留天数**、**日志目录**与占用体积、**打开日志目录**按钮。
- **隐私**：是否保存截图、保存多少天、是否对敏感区域模糊、是否允许上传结构化数据（v1 默认全关，纯本地）。
- **单实例锁**状态、**崩溃重启历史**、**关于**（版本、模型 sha256、策略版本、许可声明）。

### 3.9 托盘与全局快捷键

**3.9.1 页面职责**：软件 99% 时间不在前台，这是唯一入口。

**3.9.2 核心功能清单**

- **托盘图标颜色即状态**：灰=观察/暂停，蓝=自动正常，红=有异常或未授权，黄闪=刚点过一次。
- **托盘菜单**：暂停 10/30/60 分钟、恢复、打开主窗口、运行自检、刚才那个不该点、退出。
- **全局快捷键**（必须注册，且要处理与 IM 冲突时的降级提示）：
  - 暂停/恢复（最高优先级，演示场景救急）
  - 冻结当前帧
  - **"刚才那个不该点"**——点击发生后 3 秒内有效，一步完成误报标注 + 拉黑。这是纠错成本最低的设计。
- **边界条件**：快捷键被其他应用占用 → 设置页明确提示并允许改键；注册失败不阻止软件启动。

### 3.10 桌面叠加层 Overlay（可选，默认关闭）

**3.10.1 页面职责**：把检测框直接画在真实桌面上，用于演示与直观验证。

**3.10.2 核心功能清单**

- 非激活窗口（`NSPanel` + `.nonactivatingPanel`）、`ignoresMouseEvents = true`、不抢焦点、不进 Mission Control、不出现在任务切换器。
- **自激防护（硬约束）**：叠加层会被自己的抓屏拍到 → 模型可能把框当目标 → 再画框 → 正反馈。macOS 必须用 `SCStreamConfiguration.excludedWindows` 排除自身全部窗口；**Windows 侧用 `SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE=0x11)`（Windows 10 2004+）实现等价排除**（见第 9.1 / 4.3.7）。**不处理会出现"越点越乱、重启就好"且极难定位的 bug。**
- 同样地，**自检探针窗口也必须排除**，否则探针会触发"检测到新弹窗 → 推理"形成自激。
- 默认关闭，仅维护者可在调试台开启，且开启时状态条常驻提示。

---

## 4. 业务规则与逻辑

### 4.1 认证与权限规则

**本产品无网络账号体系**，"权限"分两层：

**（a）本机角色**

| 能力 | 普通使用者 | 维护者 |
|---|---|---|
| 查看监控台 / 统计 | ✅ | ✅ |
| 暂停 / 恢复 | ✅ | ✅ |
| 标记误报、纠错快捷键 | ✅ | ✅ |
| 切换运行模式（观察↔自动） | ❌ | ✅ |
| 编辑策略、发布 | ❌ | ✅ |
| 切换模型、运行自检 | ❌ | ✅ |
| 数据集导出 | ❌ | ✅ |
| 关闭禁区保护开关 | ❌ | ✅（留痕） |

判定方式：`config.maintainer = true` 或本机用户在指定组。v1 不做鉴权强度设计（内部工具）。

**（b）操作系统权限（真正的硬门禁）**

| 系统权限 | 用途 | 缺失后果 | 检测方式 |
|---|---|---|---|
| 屏幕录制（macOS） | 抓屏 | 抓到纯壁纸，全链路失效 | 实际截一帧判像素方差 |
| 辅助功能（macOS） | 模拟点击 | 能检出但点不动 | 发一个无害事件回读 |
| 登录项 | 开机自启 | 重启后失效 | 查询 SMAppService 状态 |

**授权变更需重启进程才生效**，必须作为一等流程实现（见 3.1.2）。

**未授权时的行为**：软件不退出、不报错弹窗，进入 `未授权` 态，托盘红色，主窗口停在引导页。

### 4.2 数据关联规则

存储采用**双写**：

- **JSONL 事件流**：不可变审计原始流，只追加，永不修改。字段最全，用于 replay。
- **SQLite**：结构化索引、查询与状态管理，供 GUI 使用。

主外键关系：

```
device_profile 1 ── n detection_event
model_version  1 ── n detection_event
policy_version 1 ── n detection_event
detection_event 1 ── n detection_box        （含被拒绝的框）
detection_event 1 ── 0..1 click_action
detection_event 1 ── 0..1 review_item
review_item     1 ── 0..1 dataset_item
app_target      1 ── n detection_event      （按 bundle id 关联）
```

**级联规则**

- 删除 `dataset_item` 不动 `review_item` 与 `event`（审计链不可断）。
- 清理过期日志（按天滚动）时，对应 `detection_event` 与 `detection_box` 一并删除，但 `stats_daily` 的聚合值保留。
- 截图文件删除时 `review_item.snapshot_path` 置空并标 `snapshot_expired`，条目本身保留。
- **禁止**因 GUI 操作删除事件流记录（只能标记 `ignored`）。

**数据快照规则（关键）**

- 每条 `detection_event` 必须冗余存储当时的 `policy_version`、`profile_id`、`model_sha256`、逐类生效阈值。
- 策略与模型变更后，历史记录**不受影响且可完整复现**。这是 replay 能成立的前提。
- `app_target` 的统计字段为累计值，不随黑名单增删回滚。

### 4.3 核心业务流程规则

**4.3.1 决策优先级链（顺序不可变，任一环节拒绝即终止并记原因）**

```
1 禁区检查     全屏应用 / 锁屏 / 投屏共享 / 远程桌面 / 电池低电 / 已暂停
2 用户空闲检测 距上次真实输入 < 1.5s → 不点（状态退回 SUSPECT，计数不清零）
3 profile 门禁 该类在本机 mode != click → 不点
4 容器约束     出口中心不在容器框内（pad = 出口短边 50%）→ blocked_outside_container
5 几何合法性   出口面积 > 容器 25% → blocked_dup；文字类重叠率 < 80% → 拒
6 逐类阈值     conf < 该类阈值 → low_conf
7 4 帧一致性   未凑齐 4 帧 或 std ≥ 1px → 继续等待，超 TTL(8s) → gave_up_timeout
8 点击预算     同容器 2s 内已点过 → suppressed_rapid；全局 > 60 次/小时 → 停手并告警
9 执行点击     → VERIFYING
```

**4.3.2 点击预算与降频**

- 同一容器特征 2 秒内只允许一次点击（防连点事故）。
- 同一 (类别, 设备) 连续失败 3 次 → 该类降频 10 分钟，并进复核队列。
- 全局每小时点击上限 60 次（可配），超限即停止动作并记 `budget_exceeded` —— 这通常意味着检测器在乱跳。

**4.3.3 消失验证**

- 点击后延迟 300ms 复截。
- 判据（取或）：该位置不再检出容器 / ROI 帧差低于阈值 / 出口框 ±10px 内不再检出同类。
- **先帧差快判，判"还在"才做一次完整推理确认**，省 CPU。
- 未消失重试：① 框扩大 1.3 倍重算中心再点（× 热区常大于可见图标）→ ② 试同类第二候选 → ③ 放弃并 `failed`。

**4.3.4 两级推理与 imgsz 规则**

- 第一级：整屏 → imgsz 640 → 只读容器类，阈值放宽（0.30~0.40），宁多勿漏。容器召回 = 系统召回上限。
- 第二级：容器框外扩 15% 裁切 → `imgsz2 = clamp(ceil32(max(crop_w, crop_h)), 768, 1280)`。
- 整屏 imgsz 由 profile 决定，基线公式 `imgsz = clamp(ceil32(0.75 × 逻辑宽), 1024, 2048)`，保证 16 逻辑像素目标在模型空间 ≥ 12px。
- 裁切块贴屏幕边缘被截断时标 `truncated`，该类阈值自动放宽 0.05。

**4.3.5 坐标与单位规则（macOS 重点）**

- 全链路内部统一使用**物理像素**。
- 模型输出 → 裁切图坐标 → 屏幕物理坐标，逆变换必须与 `popup_infer.py` 逐位对齐（`round` vs `floor`、pad 居中方式），**双实现对拍 100 张到像素一致后才算完成**。
- 发系统事件时需 像素 ÷ `backingScaleFactor` → 逻辑点。
- **GUI 中所有坐标显示必须标明单位**（推荐 px / pt 双列），否则会出现偏 2 倍且查不出原因的问题。
- 副显示器 origin 可为负值，需正确处理。

**4.3.6 帧调度与背压**

- 定时驱动（2.5~3 fps），不用 vsync 驱动。
- 有界队列容量 1~2，**丢最旧帧**，丢弃计数上报状态条与统计。
- 帧差门控：无变化区域不推理；稳态 CPU 目标 ≤ 8%，无候选时 ≤ 1%。
- 执行层全局单锁串行（点击是全局资源）。
- 推理 worker 独立进程，崩溃由主进程拉起，**重启后默认回到观察模式，不自动恢复点击权限**。

**4.3.7 自激与自我识别**

- 叠加层窗口、探针窗口必须从抓屏中排除（macOS `excludedWindows`；Windows `SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE)`，见第 9 章）。
- 纯视觉方案下这是唯一允许"知道自己是谁"的例外，需显式实现并在代码中标注。

**4.3.8 数据回流规则**

- 训练集须含 25~30% 零标注负帧；负帧采集开关按间隔静默保存"无候选"帧，感知哈希去重。
- 增强约束（写入训练规范）：`mosaic/mixup/cutmix/degrees/shear/perspective/flipud = 0`；`hsv_s/hsv_v ≤ 0.1`（若引入可用态/禁用态类别）；`erasing ≤ 0.1`；随机 scale 0.7~1.5 覆盖缩放差异。
- 验证集按弹窗来源分组切分（防近重复虚高），另设 100% 纯负帧验证集。
- 文字类出口必须真实样本，合成贴图仅对图标类/容器类有效。

### 4.4 状态机规则

**（a）软件自身状态**

```
未安装引导 → 未授权 → 待自检 → 自检中 → 自检未通过(全类 report)
                                   ↓
                              观察模式 ⇄ 自动模式
                                   ↓
                              已暂停(定时/手动) / 已降级(worker 崩溃重启后)
```

| 状态 | 允许 | 禁止 |
|---|---|---|
| 未授权 | 抓屏测试、跳系统设置 | 一切检测与点击 |
| 观察模式 | 检测、日志、上报、复核 | 真实点击 |
| 自动模式 | 全部 | 未通过自检时不可进入 |
| 已暂停 | 日志、查看 | 抓屏与推理（省资源） |
| 已降级 | 检测与日志 | 点击，直到人工确认恢复 |

**（b）弹窗处置状态机**

```
IDLE ─检出候选→ SUSPECT ─4帧达标→ STABLE ─策略允许→ ARMED
     → CLICKING → VERIFYING ─消失→ CLOSED
                          ├─未消失→ RETRY(<3) → ARMED
                          └─仍失败→ FAILED → COOLDOWN(拉黑 N 分钟)
```

**抢占规则（任意状态可被打断，回 IDLE 并记 `preempted_cause`）**：用户自行关闭、弹窗自行消失、焦点切换、屏幕内容大幅变化、进入禁区场景、用户按暂停。

**漏检帧处理**：连续 2 帧未检出才重置计数；单帧漏检保持计数但不 +1。

**CLOSED 后置检查**：回看一次，确认未连带关闭其他窗口（部分弹窗 × 会连带关掉整个应用）。

### 4.5 数据展示与计算规则

**统计口径（必须唯一定义，否则各页数字对不上）**

| 指标 | 定义 |
|---|---|
| 弹窗总数 | 唯一容器特征数（同一容器多帧只算一次，按 `truncated`+尺寸+bundle 归并） |
| 自动关闭数 | `click_action.verify_result = vanished` 的事件数 |
| 误点数 | `action = click` 且（`result = failed` 或 被用户标 `not_popup`） |
| 每千小时误点率 | 误点数 ÷ 软件运行总小时 × 1000 |
| 点击命中率 | 预测中心落入 GT 框的事件数 ÷ 匹配上的 GT 数 |
| 每千张负帧误检数 | 纯负帧上的检测数 ÷ 负帧数 × 1000 |
| 响应延迟 P95 | 容器首次稳定出现 → 点击成功的时间差 |
| 源头抑制成功率 | 同一 bundle 近 7 天弹窗次数曲线的衰减率 |

**展示规则**

- 排序：事件流按时间倒序；统计列表按计数倒序，同数按名称。
- 空值：耗时/偏差无值显示 `—`，不显示 0（0 是合法值，会造成误读）。
- 数值精度：conf 2 位小数，耗时整数 ms，偏差 1 位小数。
- 默认时间窗：统计页近 7 天；事件流近 24 小时。
- 颜色语义全局统一：绿=已点且成功，黄=已点但结果未知/重试，红=误点/异常，蓝=已拒绝（正常行为）。

---

## 5. 数据结构说明

### 5.1 核心表结构

存储：SQLite（本地单文件，`~/Library/Application Support/ScreenGuard/guard.db`）+ JSONL 事件流。

| 表名 | 说明 | 关键字段 |
|---|---|---|
| `device_profile` | 自检产物，per 显示器配置 | `id, fingerprint UNIQUE, created_at, imgsz, capture_backend, capture_ok, per_class(JSON), bias_px, std_px, capture_ms, model_ms, model_sha256, status` |
| `model_version` | 权重版本管理 | `id, name, file_path, sha256 UNIQUE, arch, params, train_imgsz, classes(JSON), metrics(JSON), enabled, created_at` |
| `policy_version` | 策略版本，只增不改 | `id, version UNIQUE, yaml_hash, content(JSON), published_at, author, note` |
| `app_target` | 按应用画像 | `id, bundle_id UNIQUE, display_name, first_seen, times_seen, times_clicked, times_failed, trust_score, mode_override` |
| `detection_event` | 一次决策（核心表） | `id, ts, session_id, profile_id FK, model_id FK, policy_version FK, frame_index, screen_w, screen_h, capture_ms, model_ms, total_ms, action, result, preempted_cause` |
| `detection_box` | 原始框，含被拒的 | `id, event_id FK, cls, conf, xyxy(JSON), role(container/exit), gate_passed, reject_reason` |
| `click_action` | 真实点击记录 | `id, event_id FK UNIQUE, point_px(JSON), point_pt(JSON), scale_factor, executed_at, verify_result, retry_n, latency_ms` |
| `review_item` | 复核队列 | `id, event_id FK, reason, snapshot_path, status, label_correction(JSON), resolved_at` |
| `dataset_item` | 待标注/已标注 | `id, review_item_id FK, image_path, label_path, state, batch_id` |
| `blacklist` | 拉黑 | `id, feature_type, feature_value, reason, created_at, expires_at, source` |
| `stats_daily` | 日聚合 | `date UNIQUE, popups_seen, auto_closed, false_positives, avg_latency_ms, top_apps(JSON), crash_count` |
| `runtime_log` | 崩溃/重启/心跳 | `id, ts, level, kind, message, stack` |

**注意事项**

- `UNIQUE`：`device_profile.fingerprint`（同一显示器配置只留最新，旧的转历史）、`model_version.sha256`（防重复导入）、`policy_version.version`、`app_target.bundle_id`、`stats_daily.date`。
- **外键**：`detection_box.event_id → detection_event.id ON DELETE CASCADE`（日志清理时一起删）；`click_action.event_id → detection_event.id ON DELETE CASCADE`；`review_item.event_id → detection_event.id ON DELETE SET NULL`（审计链优先，复核项可留孤）。
- **JSON 字段**：`per_class`、`classes`、`metrics`、`content`、`xyxy`、`point_px`、`label_correction`、`top_apps` —— 全部为扩展预留，避免频繁改表。
- 事件流 JSONL 与 SQLite 的关系：JSONL 为**唯一真源**，SQLite 可从 JSONL 重建。JSONL 字段比表更完整（含逐帧门控中间量）。
- 无废弃表（v1 新建）。

### 5.2 关键枚举值说明

| 字段 | 可选值 |
|---|---|
| `action` | `click` / `report_only` / `blocked_outside_container` / `blocked_dup` / `blocked_illegal_area` / `low_conf` / `disabled_class` / `suppressed_rapid` / `blocked_user_active` / `blocked_forbidden_zone` / `budget_exceeded` / `gave_up_timeout` |
| `result` | `none` / `vanished` / `retry` / `failed` / `preempted` |
| `role` | `container` / `exit` |
| `app_mode` | `observe` / `auto` / `paused` / `degraded` / `unauthorized` / `selftest_pending` / `selftest_failed` |
| `sm_state`（弹窗） | `IDLE` / `SUSPECT` / `STABLE` / `ARMED` / `CLICKING` / `VERIFYING` / `CLOSED` / `RETRY` / `FAILED` / `COOLDOWN` |
| `cls`（当前 10 类） | `TanChuang` / `GuanBi` / `GuanBi_str` / `XiaYiBu` / `QuXiao` / `TuiChu` / `TiaoGuo` / `WoBuShou` / `ZhiDaoLe` / `YunXu` |
| `cls`（规划新增，容器级负类） | `normal_window` / `system_dialog` / `install_wizard` / `toast_notification` / `context_menu` |
| `review.reason` | `failed_click` / `user_not_popup` / `user_wrong_target` / `low_conf_high_potential` / `no_exit_found` |
| `review.status` | `pending` / `labeled` / `ignored` / `exported` |
| `dataset_item.state` | `raw` / `labeled` / `exported` |
| `blacklist.feature_type` | `bundle_id` / `title_regex` / `container_size_range` |
| `policy.mode` | `click` / `report` / `disabled` |
| `performance_tier` | `powersave` / `balanced` / `aggressive` |

---

## 6. 异常与边界情况

| 场景分类 | 异常情况 | 预期处理方式 |
|---|---|---|
| **系统权限** | 未授予屏幕录制 | 进入 `未授权` 态，停在引导页；抓屏测试返回"仅壁纸"并明确报因；不静默空转 |
| **系统权限** | 已授权但进程未重启（macOS 特性） | 检测到此状态 → 提示"需重启生效" + 一键重启按钮 |
| **系统权限** | 未授予辅助功能 | 可检测可上报，但自动模式入口置灰并说明原因 |
| **系统权限** | 快捷键被其他应用占用 | 注册失败不阻止启动；设置页提示并允许改键 |
| **输入校验** | `policy.yaml` 语法/ schema 错误 | 保存前拦截并定位到行；**已生效版本继续运行，不半加载** |
| **输入校验** | 阈值超出 0.05~0.95 | 滑杆限制 + 输入框校验，提示合法区间 |
| **输入校验** | 黑名单正则非法 | 校验失败提示，禁止保存 |
| **业务限制** | 自检未通过却尝试开自动模式 | 拒绝并展示不达标原因；维护者可强制开启（二次确认 + 日志留痕） |
| **业务限制** | 同一容器 2s 内重复触发 | 吞掉，记 `suppressed_rapid` |
| **业务限制** | 每小时点击超预算 | 停止动作，记 `budget_exceeded`，托盘变红并提示 |
| **业务限制** | 弹窗在点击前被用户自己关掉 | 状态抢占，回 IDLE，记 `preempted_cause = user_closed` |
| **业务限制** | 点击后弹窗不消失（禁用态 × / 倒计时） | 按 4.3.3 三级重试；第 3 次失败进复核队列并降频 |
| **业务限制** | 误关了正常应用 | 纠错快捷键 3 秒窗口；托盘"刚才那个不该点"；该 bundle 进黑名单 TTL 7 天 |
| **并发与冲突** | 两个弹窗同时出现 | 按容器面积/置信度排序，一次只处理一个，处理完重新抓帧 |
| **并发与冲突** | 用户正在操作时软件想点 | 空闲检测优先，退回 SUSPECT 且不清零计数 |
| **并发与冲突** | 策略热加载与决策同时发生 | 版本号原子切换，进行中的决策用旧版本跑完 |
| **并发与冲突** | 重复启动 | 单实例锁；二次启动只唤起已有窗口 |
| **数据边界** | 事件流为空 | 空状态提示"还没有任何检测记录"，附"如何确认软件在工作"的说明 |
| **数据边界** | 复核队列为空 | 显示"没有需要复核的记录"，不显示空表格 |
| **数据边界** | 统计/导出无数据 | 提示"暂无数据可导出"，禁用导出按钮 |
| **数据边界** | 日志目录被写满或磁盘不足 | 立即停止写截图（保结构化数据），托盘告警，不崩溃 |
| **数据边界** | 截图文件被用户手工删除 | `snapshot_path` 置空标 `snapshot_expired`，条目保留 |
| **第三方依赖** | `onnxruntime.dll` / dylib 缺失或版本不符 | 启动即明确报错并给出处置指引，不进入静默失败 |
| **第三方依赖** | 模型文件损坏 / sha256 不匹配 | 拒绝加载并回退上一个可用版本 |
| **第三方依赖** | 推理 worker 进程崩溃 | 主进程拉起；**重启后进入降级态，不自动恢复点击** |
| **第三方依赖** | 单帧推理超时（>3× 基线） | 跳过本轮，记 `capture_slow`；连续 5 次 → 降帧率一档 |
| **第三方依赖** | 抓屏返回黑帧（DRM/受保护内容） | 判定该窗口不可监控，跳过并记一次；不重复刷屏日志 |
| **第三方依赖** | 共享盘策略拉取失败 | 继续用本地缓存，记 warn，状态条不打扰用户 |
| **系统限制** | 全屏 App / 独立 Space（游戏、放映） | 停止检测，状态条显示"当前场景已暂停" |
| **系统限制** | 外接屏插拔 / 分辨率或缩放变化 | profile 指纹变化 → 期间不点击 → 自动重跑自检 |
| **系统限制** | 休眠唤醒 | 丢弃唤醒后前 3 秒帧（画面未稳定），重置门控计数 |
| **系统限制** | 电池模式 / 低电 | 自动降帧；< 20% 暂停检测 |
| **系统限制** | 系统时钟被改 / 时间跳变 | 以单调时钟计算延迟指标，墙钟仅用于展示 |
| **系统限制** | 笔记本降频导致耗时翻倍 | 队列丢弃率连续 > 30% 达 1 分钟 → 自动降档并记事件 |

---

## 7. 验收标准

### 7.1 首次安装与授权流程

- 新同事首启 → 按引导完成三项授权 → 全程步骤 ≤ 4 步，每步有明确成功反馈。
- 授予屏幕录制后不重启 → 软件能识别出该状态并提示重启，重启后自动继续流程。
- 未授权状态下运行 30 分钟 → CPU 占用 ≤ 1%，无任何崩溃或空转日志洪水。

### 7.2 自检与 profile

- 维护者点"运行自检" → 3 秒内完成 → 输出各档 imgsz 的 bias/std/召回/耗时与推荐档位。
- 在 1080p@100% 与 5K@150% 两类机器上分别自检 → 推荐 imgsz 不同，且均满足"16 逻辑像素目标在模型空间 ≥ 12px"。
- 拔插外接屏 → 30 秒内检测到 profile 指纹变化 → 自动重跑，期间无点击动作。
- 自检全档不达标 → 所有类别自动设为 `report`，界面写明原因，托盘不进入红色异常态。

### 7.3 核心检测与点击流程

- 维护者在调试台喂入一张含弹窗的真实截图 → 双画布正确显示原屏与 letterbox 输入 → 容器与出口框均叠加显示 → 事件流新增一条 `report_only` 记录。
- 同一张图 → Go 端输出的出口中心坐标与 `popup_infer.py` 基准输出**逐像素一致**（100 张对拍全通过）。
- 构造"弹窗外正常窗口标题栏 ×"的图 → 该框必须显示为 `blocked_outside_container` 且不执行点击。
- 自动模式下，用户移动鼠标后 1.5 秒内构造一个弹窗 → 不发生点击，事件记 `blocked_user_active`。
- 弹窗从出现到被关闭，P95 延迟 ≤ 2.0 秒。
- 点击后弹窗消失 → 事件 `result = vanished`；不消失 → 按三级重试，第 3 次记 `failed` 并进复核队列。
- 同一容器 2 秒内连续满足门控条件 5 次 → 只发生 1 次点击，其余 4 次记 `suppressed_rapid`。

### 7.4 性能与常驻

- 1080p 目标机（纯 CPU）单帧中位耗时 ≤ 400ms，实际 fps ≥ 2.5。
- 无候选目标的稳态运行，CPU 均值 ≤ 8%，峰值 ≤ 20%（帧差门控生效）。
- 观察模式连续运行 72 小时 → 零崩溃、内存增长 ≤ 20%、日志体积在保留策略内。
- 队列丢弃率 > 30% 持续 1 分钟 → 自动降档并在状态条可见。
- 关闭主窗口 → 托盘与快捷键仍可用；托盘退出 → 无残留进程。

### 7.5 策略与可观测性

- 维护者改某类阈值 → 保存 → 2 秒内生效，无需重启，进行中的决策不被打断。
- 策略 YAML 故意写错 → 保存被拦截并定位到行，线上仍跑旧版本。
- 点"模拟重放"选近 7 天 + 新策略 → 输出点击次数、新增/减少次数、落在黑名单应用的次数，且与实际日志可交叉验证。
- 任取一条历史 `click` 事件 → 详情抽屉能完整还原：当时策略版本号、模型 sha256、逐类阈值、全部原始框（含被拒框与拒因）、4 帧中心与 std、该帧截图。**缺任一项即不通过。**
- 事件流 10 万条时打开页面 → 首屏渲染 ≤ 1s，滚动无卡顿（虚拟列表生效）。

### 7.6 数据回流

- 复核队列点"标记这不是弹窗" → 生成 `review_item` + 对应 bundle 进黑名单（TTL 7 天）。
- 标注编辑器改框 → 保存 → `dataset_item.state = labeled`。
- 勾选 50 条导出 → 得到合法 YOLO zip（images/ + labels/ + classes.txt + 元信息），可直接被训练脚本读取。
- 开启负帧采集跑 1 小时 → 得到 ≥ 200 张去重后的负帧。

### 7.7 异常流程

- 删除模型文件后重启软件 → 明确报错并回退上一版本，不崩溃。
- 推理 worker 被强杀 → 主进程拉起 → 进入降级态（不自动点击）→ 人工确认后可恢复。
- 磁盘写满 → 停止写截图但继续写结构化日志，托盘告警。
- 越权：普通使用者配置文件里无 maintainer 标记 → 调试台/策略/模型管理标签完全不渲染（非置灰）。

---

## 8. 本期不实现功能

| 功能 | 原因 |
|---|---|
| 账号体系 / 登录 / 多租户 | 单机内部工具，无网络服务端需求。角色用本地配置区分即可 |
| 云端集中管理与团队报表后台 | 依赖服务端与部署成本；v1 用"日志目录 + 手工收集"替代 |
| ~~Windows 版~~ | **【已变更】2026-09-01 起提升至 v1 范围**。详见第 9 章。原"WGC sidecar"方案改为纯 Go GDI BitBlt（单一语言栈，避免引入 C# 进程）；`GetLastInputInfo` / `SendInput` / 禁区判定均已实现。 |
| 自动更新与灰度发布通道 | 内部小范围分发，先手工替换；策略热更新已覆盖 80% 需求 |
| OCR + 关键词表做文字出口语义分级 | **已由用户明确驳回**，文字类出口继续由 YOLO 直接分多类训练 |
| UIA / Accessibility 参与"是不是弹窗"的判断 | 已定纯视觉路线。仅抓屏排除自身窗口一处例外 |
| RT-DETR 作为运行时模型 | 实测输入静态锁死 1024、622ms/帧，纯 CPU 场景不可用 |
| 桌面叠加层默认开启 | 自激风险，v1 作为维护者调试选项，默认关闭 |
| 内置完整标注工具（多边形、属性、批量质检） | 只做"修正检测框"级别的最小编辑器，完整标注继续用 X-AnyLabeling |
| 弹窗"根治"能力（卸载源头软件、改注册表/启动项） | 越界且风险高，只做"勾不再提示再关闭"这一层 |
| 勾选"下次不再提示"复选框的两步点击 | 需新增 `checkbox_checked/unchecked` 类别并重训，放 v1.1（价值高但依赖模型迭代） |
| 禁用态 × 与可用态 × 的区分 | 同上，需新类别 + HSV 增强收紧，放 v1.1 |
| 容器级显式负类（`normal_window` / `system_dialog` / `install_wizard`） | 需重标数据，放 v1.1；v1 靠几何约束 + 硬编码禁区兜底 |
| macOS App Store 沙盒分发 | 沙盒内无法做全局输入模拟与自由抓屏，走企业内直装 |
| 多语言 i18n | 内部中文环境，不做英文包 |
| 误关窗口的自动还原 | macOS 无通用"撤销关闭"能力，只能靠不点错 |

---

## 9. Windows 平台实现说明（v1 范围，2026-09-01 提升）

> 变更来源：原 §8 将 Windows 列入"本期不实现，放 v1.1"。2026-09-01 按"按 Windows 桌面端软件标准交付"的要求，提升至 v1 范围。本章记录实现方式与原计划的偏差及理由，作为后续维护的唯一事实来源。

### 9.1 选型变更说明

| 原 PRD 计划（附录 A / §8） | 实际实现 | 偏差理由 |
|---|---|---|
| Windows 抓屏用 **C# WGC sidecar**（共享内存/stdout 送帧） | **纯 Go GDI BitBlt**（`internal/capture/capture_windows.go`，无 cgo、无 C# 进程） | 用户明确偏好单一语言栈、避免过度复杂；GDI 在 2.5~3fps 下足够；引入 C# 进程增加分发与维护成本 |
| Windows 无 WGC/DXGI 绑定 → 需 sidecar | 用 GDI `BitBlt` + `CAPTUREBLT` 抓虚拟桌面全屏；副屏负原点用 `SM_XVIRTUALSCREEN/YVIRTUALSCREEN` | 纯 Go syscall 即可，且 `SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE)` 满足自激防护（PRD 4.3.7） |
| 随包分发 onnxruntime dylib/dll | Windows 无 rpath，改为 `onnxruntime.dll` 与 exe **同目录** | Windows 加载器默认从 exe 目录解析 DLL |
| `CreateSession` 入参 | Windows 要求 UTF-16 `wchar_t*`，Go 侧 `syscall.UTF16PtrFromString` 转换 | macOS 用 UTF-8 `char*`；两平台 C 包装函数签名不同，但 Go 调度/后处理逻辑共享 `ort_common.go` |

### 9.2 平台解耦架构（build tag 隔离）

```
cmd/screenguard/
  platform_darwin.go    // initPlatform / newPlatformCapturer / platformIdleSeconds / platformForbiddenZone / warnBundleIdentity
  platform_windows.go   // 同上，Windows 实现
  platform_other.go     // 非 darwin/win 兜底（StubCapturer，不产出真实帧）
internal/capture/
  capture.go            // Capturer 接口 + StubCapturer
  capture_windows.go    // //go:build windows  GDI BitBlt + DPI 感知 + 自激防护
internal/click/
  click.go              // Clicker 接口
  click_windows.go      // //go:build windows  SendInput
  click_other.go        // //go:build !darwin && !windows
  click_platform_darwin.go // NewPlatformClicker 统一工厂
internal/platform/
  win/idle_windows.go   // GetLastInputInfo
  win/forbidden_windows.go // 禁区七项判定
  win/factory_windows.go    // NewCapturer
  mac/...               // macOS 原实现
internal/infer/
  ort_common.go         // 纯 Go 公共：preprocessImage / postprocess / dedupeByClass / nmsByClass / sortBoxesByConf / iou（双平台共享）
  ort_darwin.go         // //go:build darwin   cgo 指向 onnxruntime-osx
  ort_windows.go        // //go:build windows  cgo 指向 onnxruntime-win，CreateSession 用 wchar_t*
```

### 9.3 安全边界（关键）

- **Windows 没有 macOS 的 TCC 屏幕录制授权机制**，因此"禁区判定"是 Windows 端**唯一**的安全边界（PRD 4.3.1 第 1 步），必须每秒轮询并下发给引擎 `SetForbiddenZone`。
- 禁区覆盖：全屏应用 / 锁屏屏保 / 远程桌面（RDP + 常见客户端 mstsc/teamviewer/anydesk/todesk/sunlogin/rustdesk/parsec）/ UAC 安全桌面 / 系统安装器（consent/msiexec/trustedinstaller）/ 低电量(<20%)。
- "接通电源/仅电池"（`OnBattery`）**不**计入禁区——PRD 要求电池时降帧而非暂停，仅 `LowBattery(<20%)` 暂停。
- 失败兜底：空闲检测失败返回 0（"用户刚操作过"→ 不点）；权限 `IsAuthorized()` 在 Windows 恒为 true（无 TCC 概念）。

### 9.3.1 macOS 侧禁区判定（对称补齐，消除既有缺口）

原 PRD §8/附录 A 中 macOS 禁区判定是空缺的（仅依赖 TCC 授权兜底）。本次补 `internal/platform/mac/forbidden_darwin.go`，与 Windows 版对称：

- 进程名 heuristic 复用 `platform.MatchForbiddenByProcessName`（双平台共享纯逻辑，已单测）：远程桌面客户端（rdc/teamviewer/anydesk/todesk/sunlogin/rustdesk/parsec/zoom）、系统安装器（installer/pkgutil/systempreferences）、屏保（screensaver）。
- 前台 App bundle id 通过 `osascript` 读取；电池状态通过 `pmset -g batt` 解析。
- 锁屏判断在 macOS 上需 `CGSessionCopyCurrentDictionary`（cgo/ObjC），属可选增强，未实现；TCC 仍为主安全边界。
- `ForbiddenZoneState` 类型已抽到 `internal/platform`（无 build tag），mac/win 共用，`Any()`/`Reason()` 语义一致（OnBattery 不计入禁区）。

### 9.4 桌面标准能力（PRD 3.9 / 3.10）

| 能力 | 实现 | 备注 |
|---|---|---|
| 单实例 | `application.Options.SingleInstance{UniqueID:"com.screenguard.app"}` | 二次启动聚焦已有窗口 |
| 开机自启 | `wailsApp.Autostart.Enable()` → HKCU `Run` | 默认开启，符合"后台守护"定位 |
| 全局快捷键 | `GlobalShortcut.Register`：`Ctrl+Shift+P` 暂停/恢复、`Ctrl+Shift+F` 冻结、`Ctrl+Shift+Z` 撤销最近点击 | 复用 Wails `RegisterHotKey` 封装 |
| 系统托盘 | `SystemTray.New()` + `Menu`；图标颜色语义 `TrayColor`（blue/gray/red/yellow） | 菜单复用 `ScreenGuardService` 方法 |

### 9.5 验收状态（2026-09-01）

- [x] `GOOS=windows GOARCH=amd64 CC=clang CGO_ENABLED=1 go build ./...` 通过
- [x] `go vet ./...` 通过（仅 cgo `warn_unused_result` 无害警告）
- [x] 端到端产物 `build/windows/`：screenguard.exe(14.9MB) + onnxruntime.dll(11.2MB) + models/yolo26m.onnx(78MB) + configs/policy.yaml
- [x] 跨平台单测：`internal/capture`（黑帧/帧差纯逻辑）、`internal/platform`（禁区匹配/Any 排除 OnBattery）通过
- [x] 禁区类型 `ForbiddenZoneState` 抽到 `internal/platform` 共享；macOS 侧 `forbidden_darwin.go` 对称补齐（消除既有缺口，进程名 heuristic + osascript/pmset）
- [x] 真机自检工具 `cmd/winsmoke`（Windows build tag）：GDI 抓屏非黑帧 / 多显示器虚拟原点 / DPI / 禁区判定 / 空闲检测 / SendInput 坐标归一化，全部 PASS/FAIL 结构化输出，交叉编译已验证 `winsmoke.exe` 产出。真机运行命令见 `docs/windows-e2e-checklist.md` §0
- [x] 构建/验证一键脚本 `scripts/build-win.ps1`（PowerShell，固化本会话验证过的 CGO+clang 命令，含 `-RunSmoke` 真机自检开关）；图标规范 `docs/icon-spec.md`（占位图待替换为正式品牌源图）
- [x] **自激防护已接线（PRD 4.3.7 红线）**：`GDICapturer.ExcludeSelfWindows()` 在 `ApplicationStarted` 后枚举当前进程全部顶层窗口并调用 `SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE)`，避免主窗口/调试台被自身抓屏拍到形成正反馈；`Capturer` 接口与 mac/win/stub 均已实现该方法。此前仅有 `ExcludeWindow(hwnd)` 方法但未触发，现已闭环。
- [ ] **真实端到端抓屏/点击未验证**（需 Windows 真机 + 屏幕录制授权；当前环境为交叉编译，无法运行 GUI/抓屏）。真机清单见 `docs/windows-e2e-checklist.md`
- [ ] `make win` 目标需在装有 `make` + llvm-mingw 的环境执行（Makefile 已加 `win`/`win-backend`/`win-package` 目标，修正了原 `MODEL` 路径失效 bug）

---

## 附录 A：技术栈与工程约束

| 层 | 选型 | 说明 |
|---|---|---|
| 壳 | Go 1.2x + **Wails v3**（beta 线，按用户判断采用） | macOS 走 WKWebView 系统自带，无额外运行时依赖 |
| 前端 | **Vue 3 + TypeScript + Vite + Pinia** | 图表 ECharts 按需引入；YAML 编辑 CodeMirror 6；事件流虚拟列表 |
| 推理 | `yalue/onnxruntime_go`（cgo） | 需 `CGO_ENABLED=1` + Windows 侧 mingw-w64；随包分发 onnxruntime dylib/dll |
| 模型 | YOLO26 端到端导出（`nms=True`） | **Go 侧无需实现 NMS**，输出直接是 top-300 框 |
| 抓屏 macOS | ScreenCaptureKit（`SCStream`，12.3+/13+），GDI 类方案作兜底 | 支持按 window 过滤，天然给 ROI |
| 抓屏 Windows | **纯 Go GDI BitBlt**（`gdi32.dll`/`user32.dll` syscall，**无 cgo、无 C# sidecar**） | 2.5~3fps 下足够；用 `SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE)` 满足自激防护（PRD 4.3.7）。详见第 9 章"选型变更说明" |
| 点击 Windows | **纯 Go `SendInput`**（含 `MOUSEEVENTF_VIRTUALDESK` + `MOUSEEVENTF_ABSOLUTE`，坐标归一化 0..65535） | 多显示器正确性；UIPI 限制（高完整性窗口点不动）由禁区门控兜底 |
| 空闲检测 | macOS `CGEventSourceSecondsSinceLastEventType` / Windows `GetLastInputInfo` + `GetTickCount` | 失败一律返回 0（= 刚操作过 → 不点），符合附录 A 约束 3 |
| 禁区判定 | macOS 依赖 TCC 授权兜底 / **Windows 无 TCC，禁区为唯一安全边界** | 全屏应用 / 锁屏 / 屏保 / 远程桌面 / UAC 安全桌面 / 系统安装器 / 低电量(<20%)，每秒轮询 |
| 自激防护 Windows | `SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE=0x11)` | Windows 10 2004+；等价于 macOS `excludedWindows` |
| 桌面标准能力 | Wails v3 内置：单实例（`SingleInstance`）、开机自启（`Autostart`→HKCU Run）、全局快捷键（`GlobalShortcut`→`RegisterHotKey`）、系统托盘（`SystemTray`） | 不手写 Win32，复用框架 |
| 打包 Windows | `wails3.json` `info.windows` + go-winres 版本资源 + NSIS 安装器（`build/installer.nsi`） | 分发目录 = exe + onnxruntime.dll + models/yolo26m.onnx + configs/policy.yaml |
| 交叉编译 | llvm-mingw 的 `clang`（`CC=clang CGO_ENABLED=1`） | 随包分发 `onnxruntime-win-x64-1.19.2`；Windows 无 rpath，dll 与 exe 同目录 |
| 存储 | SQLite + JSONL 双写 | JSONL 为真源，SQLite 可重建 |
| 进程模型 | 主进程（UI + 策略 + 日志）+ worker 进程（抓屏 + 推理 + 执行） | 崩溃隔离；worker 重启后进降级态 |
| 包结构约束 | cgo 与 Win32/Cocoa 调用隔离在 `internal/capture/*`、`internal/infer/ort` | 状态机、门控、坐标变换、策略引擎为纯 Go，可跨平台单测 |

**三条必须写进工程规范的约束**：

1. 预览帧走 canvas 直绘，**不进 Vue 响应式**；事件流 100ms 批量合并刷新。
2. 坐标逆变换必须与 `popup_infer.py` 双实现对拍 100 张到像素一致，才算完成。
3. 任何不确定都倒向"不点"；新增放行路径必须同时新增对应的 `blocked_*` 枚举与日志字段。

---

## 附录 B：快速检查清单（自检结果）

- [x] 应用概述里是否有一句话让陌生人看懂？ → 1.2 一句话定位
- [x] 目标用户是否明确到可区分权限？ → 2.1 四类角色 + 4.1 权限矩阵表
- [x] 每个页面的功能是否足够落地（不是空话）？ → 3.x 每页均给出控件、数据来源、边界条件
- [x] 跨页面的通用规则是否已集中到第 4 章，没有散落在各处？ → 决策链、预算、坐标、状态机、统计口径全部集中在第 4 章
- [x] 核心表结构是否覆盖了主要业务流程？ → 5.1 十二张表覆盖 抓屏→检测→决策→点击→复核→数据集→统计
- [x] 异常场景是否覆盖了认证、输入、业务限制、依赖失败四大类？ → 第 6 章另加并发、数据边界、系统限制三类
- [x] 验收标准是否每条都可验证（不是主观描述）？ → 第 7 章全部带数字阈值或可判定动作
- [x] 是否明确列出了本期不做的事项？ → 第 8 章 16 项，含理由与归属版本

**待确认事项（会反向修改本文档）**

1. GUI 受众是否包含普通使用者？若只给维护者自用，监控台可整体砍掉，工作量省约 20%。
2. 出口文字的字高实际分布（决定 imgsz 基线判据 12px 是否够用）。
3. 现有数据集的近重复密度（决定分组切分方式）。
4. 正式产品名称。
