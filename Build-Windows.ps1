param(
    [ValidatePattern('^[a-p]{32}$')][string]$ExtensionId = 'agbejnpaacdhklaocalpballabcibmgm',
    [switch]$PackageOnly
)
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Install a supported Go toolchain from https://go.dev/dl/.' }
if (-not (Get-Command clang -ErrorAction SilentlyContinue)) { throw 'Install LLVM clang and add it to PATH.' }
$env:GOOS='windows'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'; $env:GO111MODULE='off'
$dist = Join-Path $PSScriptRoot 'dist'
New-Item -ItemType Directory -Force $dist | Out-Null
$payload = Join-Path $PSScriptRoot 'installer\companion.exe'
$setup = Join-Path $dist 'SpeedLimiter-Setup.exe'
if (-not $PackageOnly) {
    Push-Location host
    try { & go build -trimpath '-ldflags=-s -w -H=windowsgui' -o $payload .; if($LASTEXITCODE -ne 0){throw 'Host build failed.'} } finally { Pop-Location }
} elseif (-not (Test-Path $payload)) { throw 'Build the companion first, then sign it if applicable.' }
Push-Location installer/resources
try { & clang --target=x86_64-w64-windows-gnu -c setup-resource.s -o ../setup_windows_amd64.syso; if($LASTEXITCODE -ne 0){throw 'Resource build failed.'} } finally { Pop-Location }
Push-Location installer
try { & go build -trimpath "-ldflags=-s -w -H=windowsgui -X main.extensionID=$ExtensionId" -o $setup .; if($LASTEXITCODE -ne 0){throw 'Installer build failed.'} } finally { Pop-Location }
Get-FileHash -Algorithm SHA256 $payload, $setup
Write-Host 'Build completed. The setup is unsigned. Verify on clean Windows before distribution.'
