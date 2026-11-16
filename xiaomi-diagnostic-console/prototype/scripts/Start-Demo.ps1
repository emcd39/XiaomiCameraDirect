param([string]$Listen = '')

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$exe = Join-Path $root 'bin\diagconsole-current.exe'
$config = Join-Path $root 'config.mock.json'
if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { throw "Missing packaged executable: $exe" }
if (-not (Test-Path -LiteralPath $config -PathType Leaf)) { throw "Missing packaged config: $config" }
$runConfig = $config
if ($Listen) {
    if ($Listen -notmatch '^127\.0\.0\.1:\d{1,5}$') { throw 'Listen override must be an IPv4 loopback address and port, for example 127.0.0.1:18881' }
    $settings = Get-Content -LiteralPath $config -Raw | ConvertFrom-Json
    $settings.listen = $Listen
    $runConfig = Join-Path $root ('.config.mock.runtime-' + [guid]::NewGuid().ToString('N') + '.json')
    [System.IO.File]::WriteAllText($runConfig, ($settings | ConvertTo-Json -Depth 10), [System.Text.UTF8Encoding]::new($false))
}
Push-Location $root
try { & $exe -config $runConfig; $code = $LASTEXITCODE } finally {
    Pop-Location
    if ($runConfig -ne $config -and (Test-Path -LiteralPath $runConfig)) { Remove-Item -LiteralPath $runConfig -Force }
}
exit $code
