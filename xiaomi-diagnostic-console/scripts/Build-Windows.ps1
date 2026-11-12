param(
  [string]$GoPath = $env:GO_EXE
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($GoPath)) {
  $goCommand = Get-Command go.exe -ErrorAction SilentlyContinue
  if (-not $goCommand) { $goCommand = Get-Command go -ErrorAction SilentlyContinue }
  if ($goCommand) { $GoPath = $goCommand.Source }
}
if ([string]::IsNullOrWhiteSpace($GoPath) -or -not (Test-Path -LiteralPath $GoPath -PathType Leaf)) {
  throw 'Go not found. Install Go and add it to PATH, set GO_EXE, or pass -GoPath <path-to-go.exe>.'
}
$GoPath = (Resolve-Path -LiteralPath $GoPath).Path
Push-Location $root
try {
  $env:GOOS = 'windows'
  $env:GOARCH = 'amd64'
  $env:CGO_ENABLED = '0'
  $env:GOPROXY = 'off'
  $env:GOFLAGS = '-mod=readonly'
  if (-not (Test-Path bin)) { New-Item -ItemType Directory bin | Out-Null }
  & $GoPath test ./... -count=1
  if ($LASTEXITCODE -ne 0) { throw 'Go test failed' }
  & $GoPath build -trimpath -o bin\diagconsole-current.exe ./cmd/diagconsole
  if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
  Get-FileHash -LiteralPath bin\diagconsole-current.exe -Algorithm SHA256
} finally { Pop-Location }
