param([switch]$EnablePTZ)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$exe = Join-Path $root 'bin\diagconsole-current.exe'
$config = Join-Path $root 'config.live.json'
if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { throw "Missing packaged executable: $exe" }
if (-not (Test-Path -LiteralPath $config -PathType Leaf)) { throw "Copy config.live.example.json to config.live.json and fill in the private gateway values first." }
$settings = Get-Content -LiteralPath $config -Raw | ConvertFrom-Json
if ($settings.mock -ne $false) { throw 'Live launcher requires mock=false.' }
if ($settings.listen -notmatch '^127\.0\.0\.1:|^\[::1\]:') { throw 'Refusing to start unless listen is loopback.' }
if (-not $settings.ptz) { $settings | Add-Member -NotePropertyName ptz -NotePropertyValue ([pscustomobject]@{ enabled = $false }) }
$settings.ptz.enabled = [bool]$EnablePTZ
[System.IO.File]::WriteAllText($config, ($settings | ConvertTo-Json -Depth 8), [System.Text.UTF8Encoding]::new($false))
Write-Output "LIVE_PTZ_ENABLED=$($settings.ptz.enabled)"
Push-Location $root
try { & $exe -config $config; $code = $LASTEXITCODE } finally { Pop-Location }
exit $code
