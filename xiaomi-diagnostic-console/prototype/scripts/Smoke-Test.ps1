param([string]$Base = 'http://127.0.0.1:18880')

$ErrorActionPreference = 'Stop'
$base = $Base.TrimEnd('/')
$page = Invoke-WebRequest -Uri ($base + '/') -Method Get -UseBasicParsing
if ($page.StatusCode -ne 200 -or $page.Content -notmatch 'src="/app\.js"') { throw 'Embedded page or application script reference is missing' }
Write-Host '/ -> HTTP 200 (embedded HTML)'
$script = Invoke-WebRequest -Uri ($base + '/app.js') -Method Get -UseBasicParsing
if ($script.StatusCode -ne 200 -or $script.Content -notmatch 'renderInventory' -or $script.Content -notmatch 'selected_camera_id') { throw 'Embedded camera-selection JavaScript is missing' }
Write-Host '/app.js -> HTTP 200 (camera selector JavaScript)'
foreach ($path in @('/api/status','/api/capabilities','/api/events','/api/record/files')) {
  $response = Invoke-WebRequest -Uri ($base + $path) -Method Get -UseBasicParsing
  Write-Host ($path + ' -> HTTP ' + [int]$response.StatusCode)
  if ($response.StatusCode -ne 200) { throw "Smoke test failed: $path" }
}
Write-Host 'SMOKE_TEST=PASS (read-only requests only)'
