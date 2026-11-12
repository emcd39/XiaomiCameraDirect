$ErrorActionPreference = 'Stop'
$base = 'http://127.0.0.1:18880'
foreach ($path in @('/api/status','/api/capabilities','/api/events','/api/record/files')) {
  $response = Invoke-WebRequest -Uri ($base + $path) -Method Get -UseBasicParsing
  Write-Host ($path + ' -> HTTP ' + [int]$response.StatusCode)
  if ($response.StatusCode -ne 200) { throw "Smoke test failed: $path" }
}
Write-Host 'SMOKE_TEST=PASS (read-only requests only)'
