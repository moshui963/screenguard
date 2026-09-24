<#
.SYNOPSIS
  Publish the zip produced by package-release.ps1 to GitHub Releases.

.DESCRIPTION
  - Reads the GitHub PAT from Windows Credential Manager (git helper=manager).
  - Creates (or reuses) a Release tagged vX.Y.Z in moshui963/screenguard.
  - Uploads dist/screenguard-windows-amd64-vX.Y.Z.zip as an asset.
  Note: run only where GitHub is reachable. Through a whitelist proxy a large
        upload may fail; the zip still remains for manual upload.

.PARAMETER Version
  Version; defaults to version.txt.
.PARAMETER ZipPath
  Explicit zip path; defaults to dist/screenguard-windows-amd64-v<ver>.zip.
.PARAMETER Repo
  Target repo; default moshui963/screenguard.
#>
param(
    [string]$Version,
    [string]$ZipPath,
    [string]$Repo = 'moshui963/screenguard'
)

$ErrorActionPreference = 'Stop'
$ProjectRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
if (-not $Version) { $Version = (Get-Content (Join-Path $ProjectRoot 'version.txt') -Raw).Trim() }
$Tag = "v$Version"
if (-not $ZipPath) { $ZipPath = Join-Path $ProjectRoot "dist\screenguard-windows-amd64-v$Version.zip" }

# 1) PAT from Windows Credential Manager via GCM manager
Write-Host "[1/4] Reading GitHub credential from Windows Credential Manager..."
$credOut = ("protocol=https`nhost=github.com`n") | & git -c credential.helper=manager credential fill 2>$null
$PAT = ($credOut | Where-Object { $_ -match '^password=(.*)' } | ForEach-Object { $Matches[1] }) -join ''
if (-not $PAT) { throw "No GitHub PAT found in Credential Manager (expect git:https://github.com)" }
Write-Host "      credential obtained (length $($PAT.Length))"

# 2) validate zip
if (-not (Test-Path $ZipPath)) { throw "Cannot find $ZipPath; run package-release.ps1 first" }
$zipMB = "{0:N1}" -f ((Get-Item $ZipPath).Length / 1MB)
Write-Host "[2/4] Asset: $ZipPath ($zipMB MB)"

$headers = @{ Authorization = "Bearer $PAT"; Accept = "application/vnd.github+json" }
$api = "https://api.github.com/repos/$Repo"

# 3) create or reuse release
Write-Host "[3/4] Creating/reusing Release $Tag ..."
$releaseBody = @"
## ScreenGuard v$Version

Local-AI desktop misclick guard for Windows. Pure-Go dynamic loading of ONNX Runtime, **no C compiler**, CGO_ENABLED=0.

### Highlights
- Shield 9-step safety decision chain (forbidden zone / idle / policy / container / geometry / threshold / frame-consistency / click-budget / execute)
- Bolt burst mode: hunts the close button at up to 8fps while a popup is on screen; end-to-end latency dropped from 5-7s to ~1s
- Block self-click guard: never clicks its own window close button
- All logs (runtime / event stream / crash stack) unified under the app's log/ folder
- MIT licensed

### Usage
Extract and double-click screenguard.exe. See the [README](https://github.com/$Repo).
"@

$rel = $null
try {
    $rel = Invoke-RestMethod -Uri "$api/releases/tags/$Tag" -Headers $headers -TimeoutSec 30
    Write-Host "      existing release reused (id=$($rel.id))"
} catch {
    $body = @{ tag_name = $Tag; name = "ScreenGuard v$Version"; body = $releaseBody; draft = $false; prerelease = $false } | ConvertTo-Json -Compress
    $rel = Invoke-RestMethod -Uri "$api/releases" -Method Post -Headers $headers -Body $body -ContentType 'application/json' -TimeoutSec 30
    Write-Host "      release created (id=$($rel.id))"
}

# 4) upload asset
Write-Host "[4/4] Uploading asset (this may take a while)..."
$assetName = Split-Path $ZipPath -Leaf
$uploadUrl = "https://uploads.github.com/repos/$Repo/releases/$($rel.id)/assets?name=$assetName"
Invoke-RestMethod -Uri $uploadUrl -Method Post -Headers $headers -InFile $ZipPath -ContentType 'application/zip' -TimeoutSec 900
Write-Host ""
Write-Host "Done: https://github.com/$Repo/releases/tag/$Tag"
