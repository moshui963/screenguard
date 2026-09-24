<#
.SYNOPSIS
  Update the GitHub repository "About" section (description, homepage, topics).

.DESCRIPTION
  - Reads the GitHub PAT from Windows Credential Manager (git helper=manager).
  - PATCHes api.github.com/repos/{owner}/{repo} with a bilingual description,
    homepage URL, and topics.
  - Safe to run multiple times (idempotent).

.PARAMETER Repo
  Target repo; default moshui963/screenguard.
#>
param([string]$Repo = 'moshui963/screenguard')

$ErrorActionPreference = 'Stop'

# 1) PAT from Windows Credential Manager via GCM
Write-Host "[1/3] Reading GitHub credential from Windows Credential Manager..."
$gitExe = 'C:\Users\14287\.workbuddy\binaries\PortableGit\versions\1.2.0\cmd\git.exe'
if (-not (Test-Path $gitExe)) { $gitExe = (Get-Command git -ErrorAction SilentlyContinue).Source }
if (-not $gitExe) { throw "git.exe not found" }
$credOut = ("protocol=https`nhost=github.com`n") | & $gitExe -c credential.helper=manager credential fill 2>$null
$PAT = ($credOut | Where-Object { $_ -match '^password=(.*)' } | ForEach-Object { $Matches[1] }) -join ''
if (-not $PAT) { throw "No GitHub PAT found in Credential Manager (expect git:https://github.com)" }
Write-Host "      credential obtained (length $($PAT.Length))"

# 2) Build payload. GitHub description limit is 350 characters.
# Bilingual, concise, feature-rich.
$description = "Windows 本地 AI 弹窗自动关闭工具 / Local AI desktop popup auto-closer for Windows. Pure-Go ONNX Runtime (CGO_ENABLED=0, no C compiler), YOLOv26 end-to-end detection, 9-step safety chain, burst mode ~1s, self-click guard, full audit trail."
$homepage = "https://github.com/$Repo"
$topics = @('windows', 'popup-blocker', 'ai', 'yolo', 'onnxruntime', 'pure-go', 'desktop-app', 'wails', 'privacy', 'automation')

Write-Host "[2/3] Updating repository metadata for $Repo ..."
$headers = @{ Authorization = "Bearer $PAT"; Accept = 'application/vnd.github+json' }
$body = @{ name = 'screenguard'; description = $description; homepage = $homepage; private = $false; has_issues = $true; has_discussions = $false; has_wiki = $false; topics = $topics } | ConvertTo-Json -Compress

$null = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo" -Method Patch -Headers $headers -Body $body -ContentType 'application/json' -TimeoutSec 30

# 3) Verify
Write-Host "[3/3] Verifying..."
$info = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo" -Headers $headers -TimeoutSec 30
Write-Host "      description: $($info.description)"
Write-Host "      homepage: $($info.homepage)"
Write-Host "      topics: $($info.topics -join ', ')"
Write-Host "Done: https://github.com/$Repo"
