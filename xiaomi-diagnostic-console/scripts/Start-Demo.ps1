$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$exe = Join-Path $root 'bin\diagconsole-current.exe'
$config = Join-Path $root 'config.mock.json'
if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { throw "Chybi $exe. Spustte scripts\Build-Windows.ps1" }
Write-Host 'Demo: http://127.0.0.1:18880'
Write-Host 'Zastaveni: Ctrl+C'
& $exe -config $config
exit $LASTEXITCODE
