<#
.SYNOPSIS
  Package ScreenGuard into a ready-to-run Windows release zip.

.DESCRIPTION
  1. (optional) Rebuild via build-win.ps1 so build/windows is fresh.
  2. Stage runtime files into a versioned staging folder.
  3. Compress to dist/screenguard-windows-amd64-vX.Y.Z.zip.
  Excludes: log/ (runtime logs), frameprobe.exe / winsmoke.exe (dev tools).

.PARAMETER NoBuild
  Skip rebuild; package current build/windows as-is.

.PARAMETER Version
  Explicit version; defaults to version.txt.
#>
param(
    [switch]$NoBuild,
    [string]$Version
)

$ErrorActionPreference = 'Stop'
$ProjectRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
$BuildDir   = Join-Path $ProjectRoot 'build\windows'
$DistDir    = Join-Path $ProjectRoot 'dist'
$Log        = Join-Path $DistDir 'package-release.log'

if (-not $Version) {
    $Version = (Get-Content (Join-Path $ProjectRoot 'version.txt') -Raw).Trim()
}
$ZipName = "screenguard-windows-amd64-v$Version.zip"
$ZipPath = Join-Path $DistDir $ZipName
$StageRoot = Join-Path $DistDir "stage\screenguard-windows-amd64-v$Version"

function Log($msg) {
    $line = "[{0:HH:mm:ss}] $msg" -f (Get-Date)
    Add-Content -Path $Log -Value $line -Encoding utf8
    Write-Host $line
}

New-Item -ItemType Directory -Force -Path $DistDir | Out-Null
Set-Content -Path $Log -Value "" -Encoding utf8

# 1) build unless -NoBuild
if (-not $NoBuild) {
    Log "Step 1/3 Building (build-win.ps1 -SkipFrontend)..."
    & (Join-Path $PSScriptRoot 'build-win.ps1') -SkipFrontend
    if ($LASTEXITCODE -ne 0) { throw "build failed (exit=$LASTEXITCODE)" }
} else {
    Log "Step 1/3 Skipping build (-NoBuild)"
}

if (-not (Test-Path (Join-Path $BuildDir 'screenguard.exe'))) {
    throw "screenguard.exe not found in $BuildDir; build first"
}

# 2) stage runtime files
Log "Step 2/3 Staging to $StageRoot"
Remove-Item -Recurse -Force $StageRoot -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $StageRoot | Out-Null

$copyItems = @(
    'screenguard.exe',
    'onnxruntime.dll',
    'tray_blue.png', 'tray_gray.png', 'tray_red.png', 'tray_yellow.png'
)
foreach ($item in $copyItems) {
    $src = Join-Path $BuildDir $item
    if (Test-Path $src) { Copy-Item $src $StageRoot -Force }
}

foreach ($dir in @('models', 'configs', 'images')) {
    $src = Join-Path $BuildDir $dir
    if (Test-Path $src) { Copy-Item $src (Join-Path $StageRoot $dir) -Recurse -Force }
}

$readme = Join-Path $ProjectRoot 'README.md'
if (Test-Path $readme) { Copy-Item $readme $StageRoot -Force }

$fileCount = (Get-ChildItem $StageRoot -Recurse -File).Count
$zipSizeMB = "{0:N1}" -f ((Get-ChildItem $StageRoot -Recurse -File | Measure-Object -Property Length -Sum).Sum / 1MB)
Log "  staged: $fileCount files, about $zipSizeMB MB"

# 3) compress
Log "Step 3/3 Compressing to $ZipPath"
if (Test-Path $ZipPath) { Remove-Item $ZipPath -Force }
Compress-Archive -Path $StageRoot -DestinationPath $ZipPath -CompressionLevel Optimal

$zipMB = "{0:N1}" -f ((Get-Item $ZipPath).Length / 1MB)
Log "Done -> $ZipPath ($zipMB MB)"
Log "Next: run scripts/publish-release.ps1 to publish on GitHub Releases"
