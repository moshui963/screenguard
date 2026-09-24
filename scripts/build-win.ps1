# ============================================================
# 屏净 ScreenGuard — Windows 构建 + 验证一键脚本
# 用法（在 Windows 上，需 Go 1.21+；无需任何 C 编译器）：
#   .\scripts\build-win.ps1 -RunSmoke
# 不带 -RunSmoke 则只构建不跑真机自检。
#
# 为什么不再需要 C 编译器：
#   ONNX Runtime 改由 internal/infer 在运行时动态加载 onnxruntime.dll
#   （LoadLibrary + OrtApi 虚函数表）调用，SQLite 改用纯 Go 驱动，
#   后端整体为纯 Go，CGO_ENABLED=0 即可完整构建，且不损失任何功能。
# ============================================================
param(
    [switch]$RunSmoke = $false,             # 构建后运行 winsmoke 真机自检
    [switch]$SkipFrontend = $false          # 跳过 npm run build
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# ---- 版本号自动 +0.01（百分位自增，满 100 进位到主版本）----
# 源真值见 version.txt；每次编译本体（screenguard.exe）时自增并回写，
# 同时注入二进制（-X screenguard/internal/version.Version）与 wails3.json 资源版本。
$verFile = Join-Path $root "version.txt"
$ver = "0.10"
if (Test-Path $verFile) { $ver = (Get-Content $verFile -Raw).Trim() }
$parts = $ver -split '\.'
$major = [int]$parts[0]
$minor = if ($parts.Length -gt 1) { [int]$parts[1] } else { 0 }
$minor += 1
if ($minor -ge 100) { $major += 1; $minor = 0 }
$newVer = "$major.$('{0:D2}' -f $minor)"
Set-Content -Path $verFile -Value $newVer -NoNewline
Write-Host "[0/4] 版本号 -> v$newVer (version.txt 已自增 +0.01)" -ForegroundColor Cyan

# 同步到 wails3.json（Windows 资源版本元数据）
#
# 注意编码：PowerShell 5.1 的 Get-Content 默认按 ANSI(GBK) 解码，
# 会把 wails3.json 里的中文（产品名/版权）读成乱码，进而导致
# ConvertFrom-Json 报 “传入的对象无效，应为":"或"}"”。
# 因此必须显式 -Encoding UTF8 读取，并以无 BOM 的 UTF-8 写回，
# 避免给 Wails / 其他 JSON 工具引入 BOM 兼容问题。
$wailsCfg = Join-Path $root "wails3.json"
if (Test-Path $wailsCfg) {
    $cfg = (Get-Content $wailsCfg -Raw -Encoding UTF8) | ConvertFrom-Json
    $cfg.version = $newVer
    if (-not $cfg.info) { $cfg | Add-Member -NotePropertyName "info" -NotePropertyValue ([PSCustomObject]@{}) }
    if (-not $cfg.info.windows) { $cfg.info | Add-Member -NotePropertyName "windows" -NotePropertyValue ([PSCustomObject]@{}) }
    $cfg.info.windows | Add-Member -NotePropertyName "productVersion" -NotePropertyValue $newVer -Force
    $cfg.info.windows | Add-Member -NotePropertyName "fileVersion" -NotePropertyValue $newVer -Force
    $json = $cfg | ConvertTo-Json -Depth 10
    [System.IO.File]::WriteAllText($wailsCfg, $json, (New-Object System.Text.UTF8Encoding($false)))
}

# ---- 工具链 ----
# 后端已全为纯 Go（ONNX Runtime 运行时动态加载、SQLite 纯 Go 驱动），
# 不再需要 llvm-mingw / clang 之类的 C 编译器参与构建。
Write-Host "[toolchain] 纯 Go 构建：无需 C 编译器" -ForegroundColor DarkGray

# ---- 前端构建 ----
if (-not $SkipFrontend) {
    Write-Host "[1/4] 前端构建 (npm run build)..." -ForegroundColor Cyan
    Push-Location frontend
    npm install
    npm run build
    Pop-Location
} else {
    Write-Host "[1/4] 跳过前端构建" -ForegroundColor Yellow
}

# ---- 后端构建（纯 Go，无需 C 编译器）----
Write-Host "[2/4] 后端构建 (纯 Go, CGO_ENABLED=0)..." -ForegroundColor Cyan
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"

$buildDir = Join-Path $root "build\windows"
New-Item -ItemType Directory -Force -Path $buildDir | Out-Null

# 主程序（注入版本号）
go build -ldflags "-s -w -X screenguard/internal/version.Version=$newVer" -o (Join-Path $buildDir "screenguard.exe") ./cmd/screenguard
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# 真机自检工具
go build -ldflags "-s -w" -o (Join-Path $buildDir "winsmoke.exe") ./cmd/winsmoke
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# 离线探针（坐标对拍用，可选）
go build -ldflags "-s -w" -o (Join-Path $buildDir "frameprobe.exe") ./cmd/frameprobe
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# ---- 拷贝运行时依赖 ----
Write-Host "[3/4] 拷贝运行时依赖 (onnxruntime.dll / 模型 / 策略)..." -ForegroundColor Cyan
$cgoDir = Join-Path $root "third_party\onnxruntime-win-x64-1.19.2\lib"
Copy-Item (Join-Path $cgoDir "onnxruntime.dll") $buildDir -Force
New-Item -ItemType Directory -Force -Path (Join-Path $buildDir "models") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $buildDir "configs") | Out-Null
# 模型：优先 yolo模型/yolo26m.onnx，回退 ../tangchuang/
$modelSrc = Join-Path $root "yolo模型\yolo26m.onnx"
if (-not (Test-Path $modelSrc)) { $modelSrc = Join-Path $root "..\tangchuang\yolo26m.onnx" }
Copy-Item $modelSrc (Join-Path $buildDir "models\yolo26m.onnx") -Force
Copy-Item (Join-Path $root "configs\policy.yaml") (Join-Path $buildDir "configs") -Force
# 默认配置文件：应用以 -config configs/config.json 启动时读取
Copy-Item (Join-Path $root "configs\config.json") (Join-Path $buildDir "configs") -Force
# 托盘图标：与 exe 同级，避免 loadTrayIcon 因工作目录不同而找不到图标
Copy-Item (Join-Path $root "build\tray_*.png") $buildDir -Force

Write-Host "  产物目录: $buildDir" -ForegroundColor Green
Get-ChildItem $buildDir | ForEach-Object { Write-Host "    $($_.Name) ($('{0:N0}' -f $_.Length) B)" }

# ---- 真机自检 ----
Write-Host "[4/4] 真机自检 (winsmoke)..." -ForegroundColor Cyan
if ($RunSmoke) {
    & (Join-Path $buildDir "winsmoke.exe")
    $smokeExit = $LASTEXITCODE
    if ($smokeExit -eq 0) {
        Write-Host "  ✅ winsmoke 全部通过" -ForegroundColor Green
    } else {
        Write-Host "  ❌ winsmoke 存在失败项（退出码 $smokeExit）" -ForegroundColor Red
        exit $smokeExit
    }
} else {
    Write-Host "  跳过（加 -RunSmoke 可在 Windows 真机直接验证抓屏/禁区/坐标）" -ForegroundColor Yellow
}

Write-Host "`n构建完成。分发目录: $buildDir" -ForegroundColor Green
