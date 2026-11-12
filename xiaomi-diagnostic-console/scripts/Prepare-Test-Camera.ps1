$ErrorActionPreference = 'Stop'

$ConsoleRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$GatewayRoot = [System.IO.Path]::GetFullPath((Join-Path $ConsoleRoot '..\.test-env\c201-pilot\win-gateway'))
$GatewayStarter = Join-Path $GatewayRoot 'start-host-gateway.ps1'
$FfmpegPath = Join-Path $GatewayRoot 'ffmpeg.exe'
$ConfigPath = Join-Path $ConsoleRoot 'config.live.json'
$PTZGatewayExe = Join-Path $GatewayRoot 'xlg-ptz-console.next.exe'
$ExpectedGatewayHash = '316227F676E0A07A1565D234A5C1EC8E00BE9E877ACFE12A208D64C1F85EE374'

function Get-LocalSHA256([string]$Path) {
    $Hasher = [System.Security.Cryptography.SHA256]::Create()
    $Stream = [System.IO.File]::OpenRead($Path)
    try { return [BitConverter]::ToString($Hasher.ComputeHash($Stream)).Replace('-', '') }
    finally { $Stream.Dispose(); $Hasher.Dispose() }
}

if (-not (Test-Path -LiteralPath $GatewayStarter -PathType Leaf)) {
    Write-Output 'The local test-gateway starter is missing.'
    exit 5
}
if (-not (Test-Path -LiteralPath $FfmpegPath -PathType Leaf)) {
    Write-Output 'The local test FFmpeg executable is missing.'
    exit 5
}

function Find-VerifiedGateway {
    foreach ($Run in (Get-ChildItem -LiteralPath $GatewayRoot -Directory -Filter 'host-run-*' -ErrorAction SilentlyContinue | Sort-Object LastWriteTime -Descending)) {
        $ManifestPath = Join-Path $Run.FullName 'firewall-target.json'
        $PidPath = Join-Path $Run.FullName 'private\gateway.pid'
        $TokenPath = Join-Path $Run.FullName 'private\api-token.txt'
        if (-not (Test-Path -LiteralPath $ManifestPath -PathType Leaf) -or
            -not (Test-Path -LiteralPath $PidPath -PathType Leaf) -or
            -not (Test-Path -LiteralPath $TokenPath -PathType Leaf)) {
            continue
        }

        try { $Manifest = Get-Content -LiteralPath $ManifestPath -Raw | ConvertFrom-Json }
        catch { continue }
        if ([bool]$Manifest.api_exposed -or [int]$Manifest.api_port -lt 1 -or
            [string]::IsNullOrWhiteSpace([string]$Manifest.local_address) -or
            [string]::IsNullOrWhiteSpace([string]$Manifest.gateway_executable)) {
            continue
        }

        $GatewayPid = 0
        if (-not [int]::TryParse((Get-Content -LiteralPath $PidPath -Raw).Trim(), [ref]$GatewayPid) -or $GatewayPid -le 0) {
            continue
        }
        $Process = Get-Process -Id $GatewayPid -ErrorAction SilentlyContinue
        if (-not $Process -or -not $Process.Path) { continue }
        try {
            $RunCreatedUtc = $Run.CreationTimeUtc
            $ProcessStartedUtc = $Process.StartTime.ToUniversalTime()
            if ([Math]::Abs(($ProcessStartedUtc - $RunCreatedUtc).TotalSeconds) -gt 120) { continue }
        } catch { continue }
        try {
            $ActualExe = [System.IO.Path]::GetFullPath($Process.Path)
            $ExpectedExe = [System.IO.Path]::GetFullPath([string]$Manifest.gateway_executable)
        } catch { continue }
        if (-not [string]::Equals($ActualExe, $ExpectedExe, [System.StringComparison]::OrdinalIgnoreCase)) { continue }

        $ApiUrl = 'http://127.0.0.1:' + [string]$Manifest.api_port
        try {
            $Health = Invoke-WebRequest -UseBasicParsing -Uri ($ApiUrl + '/healthz') -TimeoutSec 2
            if ($Health.StatusCode -ne 200) { continue }
        } catch { continue }

        return [pscustomobject]@{
            Manifest = $Manifest
            TokenPath = $TokenPath
            ApiUrl = $ApiUrl
            RunRoot = $Run.FullName
            GatewayPid = $GatewayPid
        }
    }
    return $null
}

$Active = Find-VerifiedGateway
if (-not $Active) {
    Write-Output 'No verified running test gateway found; starting the local C201 test gateway.'
    $StartupFailure = $null
    $StartupOutput = @()
    try {
        if ((Test-Path -LiteralPath $PTZGatewayExe -PathType Leaf) -and (Get-LocalSHA256 $PTZGatewayExe) -eq $ExpectedGatewayHash) {
            $StartupOutput = @(& $GatewayStarter -FfmpegPath $FfmpegPath -ExecutablePath $PTZGatewayExe 2>&1 | ForEach-Object { $_.ToString() })
        } else {
            $StartupOutput = @(& $GatewayStarter -FfmpegPath $FfmpegPath 2>&1 | ForEach-Object { $_.ToString() })
        }
    } catch {
        $StartupFailure = $_.Exception.GetBaseException().GetType().Name
    }
    if ($StartupFailure) {
        $DiagnosticText = $StartupOutput | Where-Object { $_ -match 'xiaomi_backend event=[a-z_]+ result=error reason=[a-z_]+' } | Select-Object -Last 1
        $DiagnosticInput = if ($null -eq $DiagnosticText) { '' } else { [string]$DiagnosticText }
        $DiagnosticMatch = [regex]::Match($DiagnosticInput, 'xiaomi_backend event=([a-z_]+) result=error reason=([a-z_]+)')
        if (-not $DiagnosticMatch.Success) {
            $LatestRun = Get-ChildItem -LiteralPath $GatewayRoot -Directory -Filter 'host-run-*' -ErrorAction SilentlyContinue | Sort-Object LastWriteTime -Descending | Select-Object -First 1
            if ($LatestRun) {
                $PrivateLog = Join-Path $LatestRun.FullName 'private\gateway.stderr.log'
                if (Test-Path -LiteralPath $PrivateLog -PathType Leaf) {
                    foreach ($LogLine in (Get-Content -LiteralPath $PrivateLog -Tail 200 -ErrorAction SilentlyContinue)) {
                        $CandidateMatch = [regex]::Match([string]$LogLine, 'xiaomi_backend event=([a-z_]+) result=error reason=([a-z_]+)')
                        if ($CandidateMatch.Success) { $DiagnosticMatch = $CandidateMatch }
                    }
                }
            }
        }
        if ($DiagnosticMatch.Success) {
            Write-Output ('GATEWAY_DIAGNOSTIC event=' + $DiagnosticMatch.Groups[1].Value + ' result=error reason=' + $DiagnosticMatch.Groups[2].Value)
        }
        Write-Output ('The local test gateway did not start (' + $StartupFailure + '). See the private run log for details.')
        exit 5
    }
    $Active = Find-VerifiedGateway
    if (-not $Active) {
        Write-Output 'The gateway starter returned, but no loopback-only running gateway passed verification.'
        exit 5
    }
    Write-Output 'LOCAL_GATEWAY=started and verified (API loopback only; firewall unchanged).'
} else {
    Write-Output 'LOCAL_GATEWAY=existing verified process (API loopback only).'
}

$PTZ = @{ enabled = $false }
$PTZStage = 'route_check'
$RouteStatus = 0
$Token = (Get-Content -LiteralPath $Active.TokenPath -Raw).Trim()
$Headers = @{ Authorization = 'Bearer ' + $Token }
try {
    # This deliberately invalid, non-camera ID checks only that the managed
    # PTZ route is registered. It cannot identify a real camera or send a step.
    try {
        $Response = Invoke-WebRequest -UseBasicParsing -Uri ($Active.ApiUrl + '/v2/cameras/probe/ptz/step') -Headers $Headers -Method Get -TimeoutSec 3
        $RouteStatus = [int]$Response.StatusCode
    } catch {
        if ($_.Exception.Response) { $RouteStatus = [int]$_.Exception.Response.StatusCode }
    }
    if ($RouteStatus -eq 405) {
        $PTZ = @{ enabled = $true }
    }
} catch {
    Write-Output ('PTZ API route check failed; preview remains available. stage=' + $PTZStage + ' error=' + $_.Exception.GetBaseException().GetType().Name)
} finally {
    $Token = $null
    $Headers = $null
}

$Config = [ordered]@{
    listen = '127.0.0.1:18880'
    listen_fallback = $true
    mock = $false
    gateway_url = $Active.ApiUrl
    token_file = $Active.TokenPath
    rtsp_url_file = ''
    allowed_rtsp_hosts = @('127.0.0.1', [string]$Active.Manifest.local_address)
    ffmpeg_path = $FfmpegPath
    record_dir = Join-Path $ConsoleRoot 'recordings'
    camera_address = [string]$Active.Manifest.camera_address
    ptz = $PTZ
}
[System.IO.File]::WriteAllText($ConfigPath, ($Config | ConvertTo-Json -Depth 5), [System.Text.UTF8Encoding]::new($false))
Write-Output ('LIVE_CONFIG_READY PTZ=' + $PTZ.enabled + ' API=loopback route_status=' + $RouteStatus)
