# 屏净 ScreenGuard — Windows 真机端到端验证清单

> 适用：在 Windows 10 2004+ / 11 真机验证 `build/windows/` 分发目录（或 `make win` 产物）。
> 编译环境（交叉编译）已通过 `go build`/`go vet`；本清单覆盖**编译无法验证的运行期行为**。
> 生成日期：2026-09-01。当前环境为交叉编译，以下项均待 Windows 真机执行。

---

## 0. 前置

- [ ] Windows 10 2004+（自激防护 `WDA_EXCLUDEFROMCAPTURE` 需要）
- [ ] 已解压 `build/windows/`：`screenguard.exe` + `onnxruntime.dll` + `models/yolo26m.onnx` + `configs/policy.yaml`（同目录）
- [ ] 以**普通用户**运行（非 Administrator，避免 UIPI 导致点不动高完整性窗口）

### 0.1 真机自检（无需 GUI，先跑这个）

```powershell
# 交叉编译（在开发机）：
#   make win-smoke CC=/path/to/llvm-mingw/bin/clang.exe
#   或：CGO_ENABLED=1 CC=clang GOOS=windows go build -o build/windows/winsmoke.exe ./cmd/winsmoke

# 在 Windows 真机直接跑（覆盖 §3/§5 编译无法验证的项）：
.\winsmoke.exe
```

期望输出 6 项全 `[PASS]`：
- `DPIAware` / `Capture`（抓到非黑帧）/ `ScreenSize`（虚拟屏尺寸+原点）/ `ForbiddenZone` / `IdleDetect` / `ClickNormalize`
- 任意 `[FAIL]` 退出码为 1，先排查该项再继续 §1–§8。

**可选真实点击验证**（默认关闭，避免误点）：在屏幕上放一个"点了会消失的无害测试弹窗"，然后：
```powershell
.\winsmoke.exe -click-test 960 540 -click-test-y 540
```
会在 (960,540) 物理像素处真实点击，并用 PRD 4.3.3 的消失验证判定该处是否消失。护栏：坐标越界直接跳过；处于禁区（锁屏/远程桌面/低电等）拒绝点击；执行前 3 秒倒计时警告。

## 1. 启动与基本健康

- [ ] 双击 `screenguard.exe` 不报错，托盘出现图标（蓝色 = 自动正常）
- [ ] 托盘右键菜单：暂停 10/30/60 分钟、恢复、打开主窗口、运行自检、刚才那个不该点、退出
- [ ] 主窗口可打开、调试台画布可见（若开实时帧）
- [ ] 事件日志目录生成：`%AppData%/ScreenGuard/`（config.json / guard.db / events.jsonl）

## 2. 推理链路（用 frameprobe 离线对拍，无需屏幕录制权限）

```powershell
# 在 build/windows/ 目录内执行
# 1) 截一张含弹窗的真实图（Win+Shift+S 或截图工具），保存 shot.png
# 2) 跑检测+坐标对拍
.\screenguard.exe   # 注意：frameprobe 需单独编译，见下方说明
```

> frameprobe 当前在 `cmd/frameprobe`，交叉编译需：
> `CGO_ENABLED=1 CC=clang GOOS=windows go build -o build/windows/frameprobe.exe ./cmd/frameprobe`
> 然后：
> `.\frameprobe.exe -model models/yolo26m.onnx -img shot.png -frames 4 -auto`
>
> 验收点：
- [ ] 容器/出口检出数量合理，出口中心屏幕像素与 `popup_infer.py` 对拍误差 < 2px（PRD 附录 A 约束 2）
- [ ] `-frames 4` 时决策链最终动作为 `click`（容器门控 + 一致性门控通过）
- [ ] 单帧时停在 `report_only`（一致性门控正确拦截）

## 3. 实时抓屏（GDI BitBlt）—— 编译无法验证，必须真机

- [ ] 主窗口调试台画布显示实时屏幕内容（说明 GDI 抓屏成功，非黑帧）
- [ ] 多显示器：副屏坐标正确（虚拟桌面负原点；`VirtualOrigin()` 返回正确值）
- [ ] DPI 缩放：高分屏（150%/200%）下点击坐标不偏移（PRD 4.3.5 全链路物理像素）
- [ ] 锁屏时抓屏为黑帧 → 禁区判定 `locked` 命中 → 不点击（见 §5）

## 4. 点击（SendInput）—— 编译无法验证，必须真机

- [ ] 真实弹窗（如浏览器告警、系统通知）被自动关闭且坐标准
- [ ] 多显示器下点击落在正确显示器（依赖 `MOUSEEVENTF_VIRTUALDESK`）
- [ ] 高完整性窗口（UAC 弹窗）点不动 → 被禁区 `secure_desktop` 挡住，不误点（UIPI 限制，符合预期）

## 5. 禁区判定（Windows 安全边界）—— 每秒轮询

- [ ] 全屏游戏/放映：不点击（`fullscreen_app`）
- [ ] 锁屏/屏保：不点击（`locked`）
- [ ] 远程桌面被控（mstsc/TeamViewer/AnyDesk/ToDesk/向日葵/RustDesk/Parsec）：不点击（`remote_desktop`）
- [ ] UAC/系统安装器：不点击（`secure_desktop`）
- [ ] 电池 < 20%：暂停检测（`low_battery`）；仅电池但 > 20%：降帧不暂停（PRD 第6章）

验证方法：观察 `%AppData%/ScreenGuard/events.jsonl` 中 `preempted_cause` / `action=blocked_forbidden_zone` 是否按预期出现。

## 6. 桌面标准能力

- [ ] 单实例：第二次启动聚焦已有窗口，不重复进程
- [ ] 开机自启：重启后自动运行（HKCU `Run` 写入 `屏净 ScreenGuard`）；可在设置里关闭
- [ ] 全局快捷键：`Ctrl+Shift+P` 暂停/恢复、`Ctrl+Shift+F` 冻结、`Ctrl+Shift+Z` 撤销最近点击（3 秒窗口）
- [ ] 系统托盘图标颜色随状态变化：blue(自动)/gray(暂停)/red(异常)/yellow(刚点过)

## 7. 自激防护（WDA_EXCLUDEFROMCAPTURE）

- [ ] 若开启调试台叠加层/探针窗口，其自身不应出现在抓屏中（被 `SetWindowDisplayAffinity` 排除）
- [ ] 不出现"越点越乱、重启就好"的自激现象

## 8. 安装器（NSIS）

- [ ] `makensis build/installer.nsi` 生成 `ScreenGuard-Setup-x64.exe`
- [ ] 安装到 `%LOCALAPPDATA%\ScreenGuard\`，开始菜单有快捷方式
- [ ] 卸载干净（含开始菜单项，自启 Run 项可由程序自身清理）

---

## 已知风险 / 待办

- 当前为交叉编译环境，**§3/§4/§5 的运行期项均未经真机验证**。
- 占位图标为纯色方块，出货前替换正式品牌图（托盘颜色语义见 `internal/platform.TrayColor`）。
- macOS 侧禁区判定为进程名 heuristic（纵深防御），锁屏精确判断未实现（依赖 TCC 主边界）。
