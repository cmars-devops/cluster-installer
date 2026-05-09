# tools/build.ps1 — bootstrap a project-local Go + Wails toolchain and build
# cluster-installer.exe. No system-level Go or Wails required.
#
# Toolchain lives under %LOCALAPPDATA%\cluster-installer\tooling\ so the repo
# stays clean and the same toolchain is reused across worktrees and runs.
[CmdletBinding()]
param(
    [string]$GoVersion    = '1.23.4',
    [string]$WailsVersion = 'v2.9.2'
)
$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path $PSScriptRoot -Parent
$tooling  = Join-Path $env:LOCALAPPDATA 'cluster-installer\tooling'
$goRoot   = Join-Path $tooling 'go'
$goPath   = Join-Path $tooling 'gopath'
$goBin    = Join-Path $tooling 'bin'
$goExe    = Join-Path $goRoot 'bin\go.exe'
$wailsExe = Join-Path $goBin  'wails.exe'

foreach ($d in @($tooling, $goBin, $goPath)) {
    if (-not (Test-Path $d)) { New-Item -ItemType Directory -Force -Path $d | Out-Null }
}

# Step 1 — Portable Go ----------------------------------------------------
if (-not (Test-Path $goExe)) {
    $url = "https://go.dev/dl/go$GoVersion.windows-amd64.zip"
    $zip = Join-Path $env:TEMP "go-$GoVersion.zip"
    Write-Host "[1/4] Downloading Go $GoVersion ..."
    Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing
    Write-Host "[1/4] Extracting to $tooling ..."
    if (Test-Path $goRoot) { Remove-Item -Recurse -Force $goRoot }
    Expand-Archive -Path $zip -DestinationPath $tooling -Force
    Remove-Item $zip -ErrorAction SilentlyContinue
} else {
    Write-Host "[1/4] Go already present: $goRoot"
}

$env:GOROOT = $goRoot
$env:GOPATH = $goPath
$env:GOBIN  = $goBin
$env:PATH   = "$goRoot\bin;$goBin;" + $env:PATH
& $goExe version

# Step 2 — Wails CLI ------------------------------------------------------
if (-not (Test-Path $wailsExe)) {
    Write-Host "[2/4] Installing Wails CLI $WailsVersion ..."
    & $goExe install "github.com/wailsapp/wails/v2/cmd/wails@$WailsVersion"
    if (-not (Test-Path $wailsExe)) {
        throw "Wails CLI install failed — expected $wailsExe"
    }
} else {
    Write-Host "[2/4] Wails CLI already present: $wailsExe"
}
& $wailsExe --version

# Step 3 — Build the app --------------------------------------------------
$appDir = Join-Path $repoRoot 'app'
if (-not (Test-Path (Join-Path $appDir 'wails.json'))) {
    throw "app/wails.json not found at $appDir"
}
Write-Host '[3/4] Building cluster-installer.exe ...'
Push-Location $appDir
try {
    & $wailsExe build -clean
    if ($LASTEXITCODE -ne 0) { throw "wails build failed (exit $LASTEXITCODE)" }
} finally {
    Pop-Location
}

# Step 4 — Copy result to repo root --------------------------------------
$built = Join-Path $appDir 'build\bin\cluster-installer.exe'
if (-not (Test-Path $built)) { throw "build did not produce $built" }
$dst = Join-Path $repoRoot 'cluster-installer.exe'
Copy-Item $built $dst -Force
Write-Host ''
Write-Host "[4/4] Done. Built: $dst"
Write-Host '       Double-click cluster-installer.exe to run.'
