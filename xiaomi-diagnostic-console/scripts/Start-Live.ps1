$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$exe = Join-Path $root 'bin\diagconsole-current.exe'
$config = Join-Path $root 'config.live.json'
if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { throw "Chybi $exe" }
if (-not (Test-Path -LiteralPath $config -PathType Leaf)) { throw "Vytvorte config.live.json podle config.live.example.json. Nezapinajte live PTZ bez overeni PID/session." }
Write-Host 'LIVE console: http://127.0.0.1:18880'
Write-Host 'Pozn.: Neprovadi nasazeni gateway ani restart procesu.'
& $exe -config $config
exit $LASTEXITCODE
