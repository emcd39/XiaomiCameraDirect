param([string]$Base = 'http://127.0.0.1:18880')

$ErrorActionPreference = 'Stop'
$base = $Base.TrimEnd('/')
$actionHeaders = @{ 'X-XLG-Action' = '1' }
function Invoke-Action([string]$Route,[object]$Payload=$null) {
    if ($null -eq $Payload) { return Invoke-RestMethod -Uri ($base + $Route) -Method Post -Headers $actionHeaders }
    return Invoke-RestMethod -Uri ($base + $Route) -Method Post -Headers $actionHeaders -ContentType 'application/json' -Body ($Payload | ConvertTo-Json -Compress)
}
$status = Invoke-RestMethod -Uri ($base + '/api/status')
if (-not $status.mock) { throw 'ABORT: The running client is LIVE, not MOCK! No mutations executed.' }
Write-Host 'MOCK_MODE=CONFIRMED'
$inventory = Invoke-Action '/api/inventory/refresh'
if ($inventory.camera_count -ne 4 -or $inventory.c201_count -ne 2 -or $inventory.cameras.Count -ne 4) { throw 'Mock camera chooser inventory failed' }
$videoOnly = @($inventory.cameras | Where-Object { $_.model -eq 'chuangmi.camera.039a04' })[0]
$camera = @($inventory.cameras | Where-Object { $_.model -eq 'vendor.camera.future-ptz' -and $_.ptz })[0]
if ($null -eq $videoOnly -or $videoOnly.video_ready -ne $true -or $videoOnly.ptz -ne $false) { throw 'Video-only C400 mock capability was not present' }
if ($null -eq $camera -or $camera.session_active -ne $true) { throw 'Unknown-model PTZ mock capability and active session were not present' }
$cameraID = [string]$camera.id
if ([string]::IsNullOrWhiteSpace($cameraID)) { throw 'Mock inventory did not expose the unknown model PTZ ID' }
$selectedOther = Invoke-Action '/api/inventory/select' @{ camera_id=[string]$videoOnly.id }
if ($selectedOther.selected_camera_id -ne $videoOnly.id) { throw 'Mock chooser could not select the video-only camera' }
$videoOnlyHeaders = @{ 'X-XLG-Action'='1' }
try {
    $null = Invoke-RestMethod -Uri ($base + '/api/ptz/step') -Method Post -Headers $videoOnlyHeaders -ContentType 'application/json' -Body (@{camera_id=[string]$videoOnly.id;direction='left'} | ConvertTo-Json -Compress)
    throw 'Mock PTZ unexpectedly accepted the video-only camera'
} catch {
    $response = $_.Exception.Response
    if ($null -eq $response -or [int]$response.StatusCode -ne 403) { throw }
}
$selectedCamera = Invoke-Action '/api/inventory/select' @{ camera_id=$cameraID }
if ($selectedCamera.selected_camera_id -ne $cameraID) { throw 'Mock chooser could not select the unknown PTZ model' }
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
Write-Host 'MOCK_SELECTION=PASS two C201 choices, one video-only C400, and one unknown-model PTZ camera'
Write-Host 'MOCK_VIDEO_ONLY_PTZ=PASS C400 step rejected with HTTP 403'
Write-Host 'MOCK_PTZ=PASS left,right,up,down simulated for an explicitly PTZ-capable unknown model without gateway requests'
Write-Host 'MOCK_TEST=PASS (no gateway or camera interaction)'
