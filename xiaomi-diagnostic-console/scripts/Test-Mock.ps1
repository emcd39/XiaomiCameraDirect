$ErrorActionPreference = 'Stop'
$base = 'http://127.0.0.1:18880'
$actionHeaders = @{ 'X-XLG-Action' = '1' }
function Invoke-Action([string]$Route,[object]$Payload=$null) {
    if ($null -eq $Payload) { return Invoke-RestMethod -Uri ($base + $Route) -Method Post -Headers $actionHeaders }
    return Invoke-RestMethod -Uri ($base + $Route) -Method Post -Headers $actionHeaders -ContentType 'application/json' -Body ($Payload | ConvertTo-Json -Compress)
}
$status = Invoke-RestMethod -Uri ($base + '/api/status')
if (-not $status.mock) { throw 'ABORT: The running client is LIVE, not MOCK! No mutations executed.' }
Write-Host 'MOCK_MODE=CONFIRMED'
$inventory = Invoke-Action '/api/inventory/refresh'
if ($inventory.c201_count -ne 1) { throw 'Mock inventory failed' }
$cameraID = [string]$inventory.selected_camera_id
if ([string]::IsNullOrWhiteSpace($cameraID)) { throw 'Mock inventory did not select its C201 ID' }
$start = Invoke-Action '/api/video/start'
Start-Sleep -Seconds 2
$status = Invoke-RestMethod -Uri ($base + '/api/status')
if (-not $status.video.running -or $status.video.frames -lt 1) { throw 'Mock preview produced no frames' }
Write-Host ('MOCK_VIDEO=PASS frames=' + $status.video.frames)
$null = Invoke-Action '/api/video/stop'
$directions = @(
    @{ direction='left' },
    @{ direction='right' },
    @{ direction='up' },
    @{ direction='down' }
)
foreach ($step in $directions) {
    $ptz = Invoke-Action '/api/ptz/step' @{ camera_id=$cameraID; direction=$step.direction }
    if ($ptz.outcome -ne 'simulated' -or $ptz.http_write_status -ne 'simulated_only' -or $ptz.direction -ne $step.direction) {
        throw ('Mock PTZ ' + $step.direction + ' did not return a local simulation')
    }
}
$legacyLeft = Invoke-Action '/api/ptz/left' @{}
if ($legacyLeft.outcome -ne 'simulated' -or $legacyLeft.direction -ne 'left') { throw 'Legacy left endpoint compatibility failed' }
Write-Host 'MOCK_PTZ=PASS left,right,up,down simulated without gateway requests'
Write-Host 'MOCK_TEST=PASS (no gateway or camera interaction)'
