param(
    [string]$Addr = "ws://147.45.211.122:8080/ws",
    [string]$Room = "demo",
    [string]$OutputDir = "dist\xiangqi-windows-amd64"
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$targetDir = Join-Path $repoRoot $OutputDir
$exePath = Join-Path $targetDir "ruleshift-console.exe"
$zipPath = "$targetDir.zip"

New-Item -ItemType Directory -Force -Path $targetDir | Out-Null

Push-Location $repoRoot
try {
    go build -buildvcs=false -trimpath -ldflags="-s -w" -o $exePath ./cmd/console
}
finally {
    Pop-Location
}

@"
@echo off
setlocal
cd /d "%~dp0"
ruleshift-console.exe -addr $Addr -ticket mock:player-1 -room $Room
"@ | Set-Content -Encoding ASCII (Join-Path $targetDir "player-1.cmd")

@"
@echo off
setlocal
cd /d "%~dp0"
ruleshift-console.exe -addr $Addr -ticket mock:player-2 -room $Room
"@ | Set-Content -Encoding ASCII (Join-Path $targetDir "player-2.cmd")

@"
# Ruleshift Console

Double-click one of these launchers:

- player-1.cmd
- player-2.cmd

Default server:

$Addr

Default room:

$Room

Inside the console:

get
move h2e2
resign
draw
room another-room
status
quit
"@ | Set-Content -Encoding UTF8 (Join-Path $targetDir "README.txt")

if (Test-Path $zipPath) {
    Remove-Item $zipPath
}

Compress-Archive -Path (Join-Path $targetDir "*") -DestinationPath $zipPath

Write-Host "Packaged console:"
Write-Host "  $targetDir"
Write-Host "  $zipPath"
