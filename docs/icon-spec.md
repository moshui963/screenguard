# 屏净 ScreenGuard — 图标规范

> 当前 `build/appicon.png` / `build/appicon.ico` / `build/tray_*.png` 为**纯色占位图**（蓝底白框），
> 出货前需替换为正式品牌图。本文件给出尺寸规范与源图要求，提供源图后用标准工具一键产出。

## 语义（托盘颜色，见 `internal/platform.TrayColor`）

| 文件 | 语义 | 使用场景 |
|---|---|---|
| `build/tray_blue.png` | 自动模式正常 | 监控中、可点击 |
| `build/tray_gray.png` | 观察/暂停 | 已暂停或仅观察 |
| `build/tray_red.png` | 异常/未授权 | 推理失败/未授权 |
| `build/tray_yellow.png` | 刚点过一次 | 点击后 3 秒内 |
| `build/appicon.png` / `build/appicon.ico` | 主程序图标 | 窗口/任务栏/安装器 |

托盘图标建议 **32×32**（@1x）/ **64×64**（@2x），纯色块 + 一个简洁符号（如"净"字或盾形），
背景色与下表对应，保证小尺寸下可辨识。

| 颜色 | RGB | 含义 |
|---|---|---|
| blue | (30,144,255) | 自动正常 |
| gray | (140,140,140) | 暂停/观察 |
| red | (220,50,50) | 异常 |
| yellow | (240,200,40) | 刚点过 |

主程序图标建议 **256×256**（ICO 内含 16/32/48/64/128/256 多尺寸），透明背景 + 盾形/眼睛母题。

## 源图要求

- 提供 **SVG 或 512×512+ 透明 PNG** 作为母图（命名如 `brand/appicon.svg`）。
- 托盘四色可由同一母图改背景色生成，或分别提供四张。

## 生成命令（提供源图后）

```powershell
# 工具：ImageMagick（magick）或 Inkscape，任选其一
# 1) 主图标：SVG → 多尺寸 ICO
magick brand/appicon.svg -define icon:auto-resize=256,128,64,48,32,16 build/appicon.ico
magick brand/appicon.svg -resize 256x256 build/appicon.png

# 2) 托盘四色（以蓝色为例，其他三色改 fill 颜色）
magick brand/tray.svg -background none -fill "rgb(30,144,255)" -resize 64x64 build/tray_blue.png
magick brand/tray.svg -background none -fill "rgb(140,140,140)" -resize 64x64 build/tray_gray.png
magick brand/tray.svg -background none -fill "rgb(220,50,50)"   -resize 64x64 build/tray_red.png
magick brand/tray.svg -background none -fill "rgb(240,200,40)"  -resize 64x64 build/tray_yellow.png
```

> 若用 `go-winres` 管理版本资源，图标路径在 `wails3.json` 的 `info.windows.icon` 已指向 `build/appicon.ico`，
> 替换文件即可，无需改配置。

## 当前状态（2026-09-01）

- [x] ICO/PNG 占位图已生成，编译/打包链路可走通
- [ ] 正式品牌图待提供（替换上述 5 个文件）
- [ ] 替换后需在 Windows 真机确认任务栏/托盘/安装器显示正常
