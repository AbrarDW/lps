<#
.SYNOPSIS
  Build LPS on Windows (the PowerShell equivalent of the Makefile).

.DESCRIPTION
  Works in Windows PowerShell 5.1 (built into Windows 10/11) and PowerShell 7.
  Needs only Go and Node.js; `make` is not required.

.EXAMPLE
  .\build.ps1              # build the dashboard and bin\lpsd.exe
  .\build.ps1 run          # build, then start lpsd
  .\build.ps1 test         # vet + tests + frontend typecheck
  .\build.ps1 build-all    # release binaries for Windows, Linux and macOS
  .\build.ps1 installer    # dist\LPS-Setup-<version>.exe (needs Inno Setup 6)

  If PowerShell says "running scripts is disabled on this system", run:
    powershell -ExecutionPolicy Bypass -File .\build.ps1
#>
param(
    [ValidateSet('all', 'web', 'build', 'test', 'run', 'clean', 'build-all', 'installer')]
    [string]$Task = 'all'
)

$ErrorActionPreference = 'Stop'
Set-Location -Path $PSScriptRoot

function Fail([string]$Message) {
    Write-Host ""
    Write-Host "ERROR: $Message" -ForegroundColor Red
    exit 1
}

function Step([string]$Message) {
    Write-Host "==> $Message" -ForegroundColor Cyan
}

# Run a native command and stop if it fails.
function Invoke-Native([string]$What, [scriptblock]$Command) {
    & $Command
    if ($LASTEXITCODE -ne 0) { Fail "$What failed (exit code $LASTEXITCODE). See the output above." }
}

function Require-Go {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        Fail "Go was not found. Install Go 1.24 or newer from https://go.dev/dl/ and then open a NEW terminal window."
    }
}

function Require-Npm {
    if (-not (Get-Command npm -ErrorAction SilentlyContinue)) {
        Fail "npm was not found. Install Node.js 22 LTS from https://nodejs.org/ and then open a NEW terminal window."
    }
}

function Get-Version {
    if (-not (Get-Command git -ErrorAction SilentlyContinue)) { return 'dev' }
    # git writes to stderr outside a repo; don't let that stop the script.
    $old = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    $v = & git describe --tags --always --dirty 2>$null
    $ErrorActionPreference = $old
    if ($LASTEXITCODE -ne 0 -or -not $v) { return 'dev' }
    return "$v".Trim()
}

function Get-LdFlags { return "-s -w -X main.version=$(Get-Version)" }

function Build-Web {
    Require-Npm
    Step "Building the dashboard (web/)"
    Push-Location (Join-Path $PSScriptRoot 'web')
    try {
        Invoke-Native 'npm ci' { npm ci }
        Invoke-Native 'npm run build' { npm run build }
    }
    finally { Pop-Location }
}

function Build-Lpsd {
    Require-Go
    Step "Building bin\lpsd.exe"
    if (-not (Test-Path (Join-Path $PSScriptRoot 'web\dist\index.html'))) {
        Write-Host "    Note: the dashboard is not built yet; lpsd will show a hint page. Run .\build.ps1 to include it." -ForegroundColor Yellow
    }
    $ldflags = Get-LdFlags
    Invoke-Native 'go build' { go build -ldflags $ldflags -o (Join-Path $PSScriptRoot 'bin\lpsd.exe') ./cmd/lpsd }
}

# Embed the icon and version info into lps.exe (optional: skipped if go-winres is missing).
function Add-WinRes([string]$Version) {
    $num = ($Version -replace '^v', '') -replace '[^0-9.].*$', ''
    if (-not $num) { $num = '0.0.0' }
    Push-Location (Join-Path $PSScriptRoot 'cmd\lps')
    try {
        & go run github.com/tc-hib/go-winres@v0.3.3 simply --arch amd64 --icon lps.ico --manifest gui `
            --product-name LPS --file-description "LPS - Local Personal Server" `
            --product-version $num --file-version $num --copyright "LPS contributors (MIT)"
        if ($LASTEXITCODE -ne 0) { Write-Host "    Warning: go-winres failed; lps.exe will have no icon." -ForegroundColor Yellow }
    }
    finally { Pop-Location }
}

function Build-Desktop {
    Require-Go
    $v = Get-Version
    Add-WinRes $v
    Step "Building bin\lps.exe (desktop app with tray icon)"
    $ldflags = "-s -w -H windowsgui -X main.version=$v"
    $old = $env:GOOS; $env:GOOS = 'windows'
    try { Invoke-Native 'go build lps.exe' { go build -ldflags $ldflags -o (Join-Path $PSScriptRoot 'bin\lps.exe') ./cmd/lps } }
    finally { $env:GOOS = $old }
}

function Find-Iscc {
    $cmd = Get-Command iscc -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    foreach ($p in @("${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe", "$env:ProgramFiles\Inno Setup 6\ISCC.exe", "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe")) {
        if ($p -and (Test-Path $p)) { return $p }
    }
    return $null
}

function Check-Docker {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        Write-Host "    Warning: Docker was not found. Install Docker Desktop (WSL2 backend) to create VPSes and apps: https://www.docker.com/products/docker-desktop/" -ForegroundColor Yellow
    }
}

switch ($Task) {
    'web' { Build-Web }
    'build' { Build-Lpsd }
    'all' {
        Build-Web
        Build-Lpsd
        Check-Docker
        Write-Host ""
        Write-Host "Done. Start LPS with:  .\bin\lpsd.exe serve" -ForegroundColor Green
        Write-Host "Then open http://127.0.0.1:7070"
    }
    'run' {
        Build-Web
        Build-Lpsd
        Check-Docker
        Step "Starting lpsd (Ctrl+C to stop)"
        & (Join-Path $PSScriptRoot 'bin\lpsd.exe') serve
    }
    'test' {
        Require-Go
        Require-Npm
        Step "go vet"
        Invoke-Native 'go vet' { go vet ./... }
        Step "go test"
        Invoke-Native 'go test' { go test ./... }
        Step "Frontend typecheck"
        Push-Location (Join-Path $PSScriptRoot 'web')
        try {
            if (-not (Test-Path 'node_modules')) { Invoke-Native 'npm ci' { npm ci } }
            Invoke-Native 'npm run typecheck' { npm run typecheck }
        }
        finally { Pop-Location }
        Write-Host "All checks passed." -ForegroundColor Green
    }
    'build-all' {
        Require-Go
        $ldflags = Get-LdFlags
        $targets = @(
            @('windows', 'amd64', '.exe'), @('windows', 'arm64', '.exe'),
            @('linux', 'amd64', ''), @('linux', 'arm64', ''),
            @('darwin', 'amd64', ''), @('darwin', 'arm64', '')
        )
        $oldOs = $env:GOOS; $oldArch = $env:GOARCH
        try {
            foreach ($t in $targets) {
                $out = Join-Path $PSScriptRoot ("bin\lpsd-{0}-{1}{2}" -f $t[0], $t[1], $t[2])
                Step "Building $out"
                $env:GOOS = $t[0]; $env:GOARCH = $t[1]
                Invoke-Native "go build ($($t[0])/$($t[1]))" { go build -ldflags $ldflags -o $out ./cmd/lpsd }
            }
        }
        finally { $env:GOOS = $oldOs; $env:GOARCH = $oldArch }
    }
    'installer' {
        $iscc = Find-Iscc
        if (-not $iscc) { Fail "Inno Setup 6 was not found. Install it from https://jrsoftware.org/isdl.php (or: winget install JRSoftware.InnoSetup), then try again." }
        Build-Web
        Build-Lpsd
        Build-Desktop
        $v = (Get-Version) -replace '^v', ''
        Step "Building the installer with Inno Setup"
        Invoke-Native 'iscc' { & $iscc "/DAppVersion=$v" (Join-Path $PSScriptRoot 'installer\windows\lps.iss') }
        Write-Host ""
        Write-Host "Done: dist\LPS-Setup-$v.exe" -ForegroundColor Green
    }
    'clean' {
        $bin = Join-Path $PSScriptRoot 'bin'
        if (Test-Path $bin) { Remove-Item -Recurse -Force $bin }
        Write-Host "Removed bin\"
    }
}
