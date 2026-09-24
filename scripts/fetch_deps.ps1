# ============================================================
# 屏净 ScreenGuard — 一次性拉取 ONNX Runtime (Windows x64)
# 只需运行一次；之后 build-win.ps1 会自动引用 third_party/ 下的 onnxruntime.dll
# ============================================================
param(
    [string]$Version = "1.19.2",
    [string]$TargetDir = "third_party"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$url  = "https://github.com/microsoft/onnxruntime/releases/download/v$Version/onnxruntime-win-x64-$Version.zip"
$dest = Join-Path $root $TargetDir
New-Item -ItemType Directory -Force -Path $dest | Out-Null
$zip  = Join-Path $dest "onnxruntime-win-x64-$Version.zip"
$out  = Join-Path $dest "onnxruntime-win-x64-$Version"

if (Test-Path (Join-Path $out "lib/onnxruntime.dll")) {
    Write-Host "✅ onnxruntime 已存在，跳过下载: $out" -ForegroundColor Green
    exit 0
}

Write-Host "⬇️  下载 onnxruntime v$Version (~60MB) ..." -ForegroundColor Cyan
Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing

Write-Host "📦 解压到 $out ..." -ForegroundColor Cyan
Expand-Archive -Path $zip -DestinationPath $dest -Force
Remove-Item $zip -Force

if (Test-Path (Join-Path $out "lib/onnxruntime.dll")) {
    Write-Host "✅ 完成: $out\lib\onnxruntime.dll" -ForegroundColor Green
} else {
    Write-Host "❌ 解压后未找到 onnxruntime.dll，请检查 $out" -ForegroundColor Red
    exit 1
}
