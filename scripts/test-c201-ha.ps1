[CmdletBinding()]
param(
    [switch]$KeepStack,
    [switch]$KeepOnFailure,
    [ValidateRange(10, 600)] [int]$InitialStreamSeconds = 60,
    [ValidateRange(60, 1800)] [int]$SoakSeconds = 600
)

$ErrorActionPreference = 'Stop'
$Wslc = 'C:\Program Files\WSL\wslc.exe'
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$TestRoot = Join-Path $RepoRoot '.test-env'
$EvidenceRoot = Join-Path $TestRoot 'evidence'
$RunId = (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + ([guid]::NewGuid().ToString('N').Substring(0, 8))
$SubnetOctet = ([Convert]::ToInt32($RunId.Substring($RunId.Length - 2, 2), 16) % 254) + 1
$NetworkSubnet = "10.250.$SubnetOctet.0/24"
$NetworkGateway = "10.250.$SubnetOctet.1"
$GatewayIP = "10.250.$SubnetOctet.2"
$RunRoot = Join-Path $TestRoot "run-c201-$RunId"
$PrivateRoot = Join-Path $RunRoot 'private'
$HaConfig = Join-Path $RunRoot 'ha-config'
$GatewayData = Join-Path $RunRoot 'gateway-data'
$Evidence = Join-Path $EvidenceRoot "test-c201-ha-$RunId.log"
$Network = "xlg-c201-$RunId-net"
$HaContainer = "xlg-c201-$RunId-ha"
$GatewayContainer = "xlg-c201-$RunId-gateway"
$GatewayImage = "xlg-gateway-c201:$RunId"
$HaImage = 'ghcr.io/home-assistant/home-assistant:2026.10.4'
$HaDigest = 'sha256:23f4dfbb69ce76315dbc4de7915504f1c2dcecd7ca2648113a493a385972e133'
$HaPort = $null
$StackCreated = $false
$GatewayStarted = $false
$HaStarted = $false
$Succeeded = $false
$Failure = $null
$HaUsername = "c201_test_$RunId" -replace '-', '_'
$HaPassword = $null
$ApiToken = $null

New-Item -ItemType Directory -Force -Path $EvidenceRoot, $RunRoot, $PrivateRoot, $HaConfig, $GatewayData | Out-Null
Set-Content -LiteralPath $Evidence -Value "C201 Home Assistant physical pilot $RunId" -Encoding UTF8

function Write-Evidence([string]$Line) {
    Add-Content -LiteralPath $Evidence -Value $Line -Encoding UTF8
    Write-Host $Line
}

function Set-PrivateAcl([string]$Path) {
    $identity = [System.Security.Principal.WindowsIdentity]::GetCurrent()
    $acl = Get-Acl -LiteralPath $Path
    $acl.SetAccessRuleProtection($true, $false)
    $rule = [System.Security.AccessControl.FileSystemAccessRule]::new(
        $identity.User,
        [System.Security.AccessControl.FileSystemRights]::FullControl,
        [System.Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [System.Security.AccessControl.InheritanceFlags]::ObjectInherit,
        [System.Security.AccessControl.PropagationFlags]::None,
        [System.Security.AccessControl.AccessControlType]::Allow
    )
    $acl.SetAccessRule($rule)
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Get-FreeLoopbackPort {
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    try { $listener.Start(); return $listener.LocalEndpoint.Port }
    finally { $listener.Stop() }
}

function New-RandomHex([int]$ByteCount) {
    $bytes = New-Object byte[] $ByteCount
    $generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($bytes)
        return [System.BitConverter]::ToString($bytes).Replace('-', '')
    } finally {
        [Array]::Clear($bytes, 0, $bytes.Length)
        $generator.Dispose()
    }
}

function Invoke-Wslc([string]$Phase, [string[]]$Arguments) {
    Write-Evidence "`n=== $Phase ==="
    $previousErrorActionPreference = $ErrorActionPreference
    try {
        # WSLc writes build progress to stderr. Windows PowerShell 5.1 turns
        # native stderr records into terminating errors under Stop, even when
        # the process itself succeeds.
        $ErrorActionPreference = 'Continue'
        $output = @(& $Wslc @Arguments 2>&1)
        $code = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    foreach ($line in $output) {
        $text = [string]$line
        Add-Content -LiteralPath $Evidence -Value $text -Encoding UTF8
        Write-Host $text
    }
    Write-Evidence "EXIT_CODE[$Phase]=$code"
    if ($code -ne 0) { throw "$Phase failed with exit code $code" }
    return ,$output
}

function Get-WslcJson([string]$Phase, [string[]]$Arguments) {
    Write-Evidence "`n=== $Phase ==="
    $previousErrorActionPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $output = @(& $Wslc @Arguments 2>&1)
        $code = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    Write-Evidence "EXIT_CODE[$Phase]=$code"
    if ($code -ne 0) { throw "$Phase failed with exit code $code" }
    return ,(ConvertFrom-Json (($output | ForEach-Object { [string]$_ }) -join "`n"))
}

function Write-PrivateEnv([string]$Path, [string[]]$Lines) {
    foreach ($line in $Lines) {
        if ($line -match '[\r\n]') { throw 'Private environment value contains a line break' }
    }
    [System.IO.File]::WriteAllText($Path, (($Lines -join "`n") + "`n"), [System.Text.UTF8Encoding]::new($false))
}

function Get-ViewerAccount {
    $configPath = Join-Path $env:APPDATA 'XiaomiViewer/config.json'
    if (-not (Test-Path -LiteralPath $configPath)) { throw 'The signed-in Xiaomi Viewer profile is missing' }
    $config = Get-Content -Raw -LiteralPath $configPath | ConvertFrom-Json
    $account = $config.account
    $c201 = @($config.cameras | Where-Object { $_.model -eq 'xiaomi.camera.c201' -and $_.enabled })
    if ($c201.Count -ne 1 -or -not $account.user_id -or $account.region -ne 'de') {
        throw 'The Xiaomi Viewer profile does not contain one enabled C201 in the EU region'
    }
    $stored = [string]$account.token
    if (-not $stored.StartsWith('dpapi:', [System.StringComparison]::Ordinal)) {
        throw 'The Xiaomi Viewer token is not protected by the expected Windows user encryption'
    }
    $protected = [Convert]::FromBase64String($stored.Substring(6))
    Add-Type -AssemblyName System.Security
    $clear = [System.Security.Cryptography.ProtectedData]::Unprotect(
        $protected, $null, [System.Security.Cryptography.DataProtectionScope]::CurrentUser
    )
    try {
        $token = [System.Text.Encoding]::UTF8.GetString($clear)
        if ([string]::IsNullOrWhiteSpace($token)) { throw 'The Xiaomi Viewer profile has no usable account token' }
        return [pscustomobject]@{ UserId = [string]$account.user_id; Region = [string]$account.region; Token = $token }
    } finally {
        [Array]::Clear($clear, 0, $clear.Length)
        [Array]::Clear($protected, 0, $protected.Length)
    }
}

function Wait-HomeAssistant([int]$Port) {
    $deadline = (Get-Date).AddMinutes(3)
    while ((Get-Date) -lt $deadline) {
        try {
            $response = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/" -UseBasicParsing -TimeoutSec 4
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 500) {
                Write-Evidence "HA_READY=true"
                return
            }
        } catch { Start-Sleep -Seconds 2 }
    }
    throw 'Fresh Home Assistant did not become reachable within three minutes'
}

function Wait-GatewayReady {
    $deadline = (Get-Date).AddSeconds(90)
    while ((Get-Date) -lt $deadline) {
        $previousErrorActionPreference = $ErrorActionPreference
        try {
            $ErrorActionPreference = 'Continue'
            $output = @(& $Wslc exec $GatewayContainer /bin/busybox wget -q -O - 'http://127.0.0.1:8099/healthz' 2>&1)
            $code = $LASTEXITCODE
        } finally { $ErrorActionPreference = $previousErrorActionPreference }
        if ($code -eq 0 -and $output.Count -gt 0) {
            Write-Evidence 'LINUX_GATEWAY_HEALTH=ready'
            return
        }
        Start-Sleep -Seconds 2
    }
    Write-SanitizedGatewayDiagnostics
    throw 'Linux managed gateway did not expose its local health endpoint within 90 seconds'
}

function Invoke-HaPython([string]$Phase, [string[]]$PythonArgs) {
    $arguments = @('exec', '--env-file', (Join-Path $PrivateRoot 'runtime.env'), $HaContainer, 'python3') + $PythonArgs
    Invoke-Wslc $Phase $arguments | Out-Null
}

function Confirm-E2EOutcome([string]$ExpectedPhase, [int]$MinimumSeconds = 0) {
    $resultPath = Join-Path $HaConfig '.xlg-c201-e2e-result.json'
    if (-not (Test-Path -LiteralPath $resultPath)) { throw 'C201 E2E helper did not write its result record' }
    $result = Get-Content -Raw -LiteralPath $resultPath | ConvertFrom-Json
    $expectedResultPhase = switch ($ExpectedPhase) {
        'setup' { 'setup' }
        'direct-rtsp' { 'direct_gateway_rtsp_diagnostic' }
        'snapshot' { 'ha_camera_proxy' }
        'stream' { 'ha_live_hls' }
        default { throw 'Unsupported C201 E2E result phase' }
    }
    if ($result.phase -ne $expectedResultPhase -or $result.result -ne 'ok' -or $result.model_id -ne 'xiaomi.camera.c201') {
        throw 'C201 E2E helper result phase, model, or status did not match'
    }

    if ($ExpectedPhase -in @('stream', 'direct-rtsp')) {
        $decodedMinimum = [Math]::Max(1, $MinimumSeconds - 2)
        $wallMinimum = [Math]::Max(1, $MinimumSeconds - 5)
        if ([int]$result.duration_seconds -ne $MinimumSeconds -or [int]$result.decoded_seconds -lt $decodedMinimum -or [int]$result.wall_seconds -lt $wallMinimum -or [int]$result.decoded_frames -lt 1) {
            throw 'C201 media result did not meet the decoded duration and wall-clock thresholds'
        }
        $route = if ($ExpectedPhase -eq 'stream') { 'ha_hls' } else { 'direct_gateway_rtsp' }
        Write-Evidence "C201_MEDIA_CHECK=route=$route requested_seconds=$($result.duration_seconds) decoded_seconds=$($result.decoded_seconds) wall_seconds=$($result.wall_seconds) frames=$($result.decoded_frames)"
    } elseif ($ExpectedPhase -eq 'setup') {
        Write-Evidence "C201_ENTITY_CHECK=model_id=$($result.model_id)"
    } elseif ($ExpectedPhase -eq 'snapshot') {
        if ([int]$result.jpeg_bytes -lt 1) { throw 'C201 snapshot result did not contain JPEG data' }
        Write-Evidence "C201_SNAPSHOT_CHECK=model_id=$($result.model_id) jpeg_bytes=$($result.jpeg_bytes)"
    } else {
        throw 'Unsupported C201 E2E result phase'
    }
}

function Write-SanitizedGatewayDiagnostics {
    if (-not $GatewayStarted) { return }
    try {
        $previousErrorActionPreference = $ErrorActionPreference
        try {
            $ErrorActionPreference = 'Continue'
            $lines = @(& $Wslc logs --tail 300 $GatewayContainer 2>&1)
            $logExit = $LASTEXITCODE
        } finally { $ErrorActionPreference = $previousErrorActionPreference }
        if ($logExit -ne 0) {
            Write-Evidence "DIAGNOSTIC gateway_logs_unavailable exit_code=$logExit"
            return
        }
        $eventCount = 0
        foreach ($line in $lines) {
            $text = [string]$line
            $match = [regex]::Match($text, 'xiaomi_backend event=[a-z_]+ result=[a-z_]+(?: (?:model_profile|transport|codec|camera_count|bytes|reason)=[A-Za-z0-9_+-]+)*|xiaomi_cs2 phase=(?:lan_search|punch) result=(?:ok|error) elapsed_ms=\d+ rejected_wrong_host=\d+ rejected_malformed=\d+ rejected_unmatched=\d+')
            if ($match.Success) {
                Write-Evidence "DIAGNOSTIC $($match.Value)"
                $eventCount++
            } elseif ($text -match 'Xiaomi Local Gateway listening on .*managed=true.*xiaomi_provider=true') {
                Write-Evidence 'DIAGNOSTIC gateway_start=ready managed=true xiaomi_provider=true'
                $eventCount++
            } elseif ($text -match 'Xiaomi account authorization failed') {
                Write-Evidence 'DIAGNOSTIC gateway_start=failed reason=account_authorization'
                $eventCount++
            } elseif ($text -match 'Xiaomi account configuration is incomplete') {
                Write-Evidence 'DIAGNOSTIC gateway_start=failed reason=configuration_incomplete'
                $eventCount++
            } elseif ($text -match 'Xiaomi camera backend unavailable') {
                Write-Evidence 'DIAGNOSTIC gateway_start=failed reason=backend_unavailable'
                $eventCount++
            } elseif ($text -match 'create gateway handler:') {
                Write-Evidence 'DIAGNOSTIC gateway_start=failed reason=handler_initialization'
                $eventCount++
            }
        }
        if ($eventCount -eq 0) { Write-Evidence 'DIAGNOSTIC gateway_log_events=none' }
    } catch { }
}

try {
    if (-not (Test-Path -LiteralPath $Wslc)) { throw 'WSLc is not installed at the expected path' }
    Set-PrivateAcl $RunRoot
    Set-PrivateAcl $PrivateRoot

    $viewer = Get-ViewerAccount
    $HaPassword = New-RandomHex 24
    $ApiToken = New-RandomHex 32
    $HaPort = Get-FreeLoopbackPort
    # The E2E helper runs inside the Home Assistant container, so it must use
    # the container-local listener. The printed browser URL still uses $HaPort.
    $HaUrl = 'http://127.0.0.1:8123'
    $runtimeEnv = Join-Path $PrivateRoot 'runtime.env'
    $gatewayEnv = Join-Path $PrivateRoot 'gateway.env'
    Write-PrivateEnv $runtimeEnv @(
        "HA_URL=$HaUrl"
        "HA_USERNAME=$HaUsername"
        "HA_PASSWORD=$HaPassword"
        "XLG_API_TOKEN=$ApiToken"
        "XLG_GATEWAY_URL=http://${GatewayIP}:8099"
    )
    Write-PrivateEnv $gatewayEnv @(
        "XLG_API_TOKEN=$ApiToken"
        "XLG_XIAOMI_USER_ID=$($viewer.UserId)"
        "XLG_XIAOMI_REGION=$($viewer.Region)"
        "XLG_XIAOMI_TOKEN=$($viewer.Token)"
        "XLG_RTSP_ADVERTISE_HOST=${GatewayIP}"
        'XLG_RTSP_LISTEN=:8554'
    )
    $viewer.Token = $null
    $viewer.UserId = $null

    $fixture = Join-Path $RepoRoot 'tests/fixtures/home-assistant/configuration.yaml'
    Copy-Item -LiteralPath $fixture -Destination (Join-Path $HaConfig 'configuration.yaml')
    $sourceIntegration = Join-Path $RepoRoot 'legacy/custom_components/xiaomi_local_gateway'
    $targetIntegration = Join-Path $HaConfig 'custom_components/xiaomi_local_gateway'
    New-Item -ItemType Directory -Force -Path $targetIntegration, (Join-Path $HaConfig 'tests/e2e') | Out-Null
    foreach ($file in (Get-ChildItem -LiteralPath $sourceIntegration -File -Recurse)) {
        $relative = $file.FullName.Substring($sourceIntegration.Length + 1)
        $destination = Join-Path $targetIntegration $relative
        New-Item -ItemType Directory -Force -Path (Split-Path $destination -Parent) | Out-Null
        Copy-Item -LiteralPath $file.FullName -Destination $destination
    }
    Copy-Item -LiteralPath (Join-Path $RepoRoot 'tests/e2e/test_ha.py') -Destination (Join-Path $HaConfig 'tests/e2e/test_ha.py')
    Copy-Item -LiteralPath (Join-Path $RepoRoot 'tests/e2e/test_c201_ha.py') -Destination (Join-Path $HaConfig 'tests/e2e/test_c201_ha.py')
    Copy-Item -LiteralPath (Join-Path $RepoRoot 'tests/e2e/ha_live_soak.py') -Destination (Join-Path $HaConfig 'tests/e2e/ha_live_soak.py')
    Copy-Item -LiteralPath (Join-Path $RepoRoot 'tests/e2e/xlg_soak_live.py') -Destination (Join-Path $HaConfig 'tests/e2e/xlg_soak_live.py')
    Copy-Item -LiteralPath (Join-Path $RepoRoot 'tests/e2e/test_hls_diagnostics.py') -Destination (Join-Path $HaConfig 'tests/e2e/test_hls_diagnostics.py')
    foreach ($hlsFile in @('test_c201_ha.py', 'ha_live_soak.py', 'xlg_soak_live.py')) {
        $hlsHelperSource = Join-Path $RepoRoot "tests/e2e/$hlsFile"
        $hlsHelperCopy = Join-Path $HaConfig "tests/e2e/$hlsFile"
        if ((Get-FileHash -LiteralPath $hlsHelperSource -Algorithm SHA256).Hash -cne (Get-FileHash -LiteralPath $hlsHelperCopy -Algorithm SHA256).Hash) { throw "Copied HA HLS helper differs from current checkout: $hlsFile" }
    }
    Write-Evidence 'HA_INTEGRATION_SOURCE=CURRENT_CHECKOUT'
    Write-Evidence 'XIAOMI_CREDENTIAL_SOURCE=WINDOWS_DPAPI_PROFILE'
    Write-Evidence 'MEDIA_SOURCE=CS2_C201_ONLY'
    Write-Evidence 'AUDIO_REQUESTED=false'

    $null = Invoke-Wslc 'Build gateway image from current checkout' @('build', '--progress', 'plain', '-t', $GatewayImage, $RepoRoot)
    $null = Invoke-Wslc 'Pull pinned Home Assistant image' @('pull', $HaImage)
    $haImageInfo = Get-WslcJson 'Inspect Home Assistant image digest' @('inspect', '--type', 'image', '--format', 'json', $HaImage)
    if (@($haImageInfo[0].RepoDigests) -notcontains "ghcr.io/home-assistant/home-assistant@$HaDigest") { throw 'Pulled Home Assistant image digest does not match the pinned test image' }
    Write-Evidence "HA_IMAGE_DIGEST=$HaDigest"

    $null = Invoke-Wslc 'Create isolated C201 network' @('network', 'create', '--subnet', $NetworkSubnet, '--gateway', $NetworkGateway, $Network)
    $StackCreated = $true
    $null = Invoke-Wslc 'Start managed gateway with private Xiaomi credentials' @(
        'run', '-d', '--name', $GatewayContainer, '--network', $Network, '--ip', $GatewayIP,
        '--env-file', $gatewayEnv, '-v', "${GatewayData}:/data", $GatewayImage,
        '-managed', '-listen', '0.0.0.0:8099'
    )
    $GatewayStarted = $true
    Remove-Item -LiteralPath $gatewayEnv -Force
    Wait-GatewayReady

    $null = Invoke-Wslc 'Start fresh Home Assistant' @(
        'run', '-d', '--name', $HaContainer, '--network', $Network,
        '-p', "127.0.0.1:${HaPort}:8123", '-v', "${HaConfig}:/config", $HaImage
    )
    $HaStarted = $true
    Wait-HomeAssistant $HaPort

    Invoke-HaPython 'Configure C201 through the Home Assistant integration' @('/config/tests/e2e/test_c201_ha.py', 'setup')
    Confirm-E2EOutcome 'setup'
    Invoke-HaPython 'Diagnose direct RTSP from Home Assistant to Linux gateway' @('/config/tests/e2e/test_c201_ha.py', 'direct-rtsp', '--seconds', '10')
    Confirm-E2EOutcome 'direct-rtsp' 10
    Invoke-HaPython 'Verify decoded C201 snapshot through HA camera proxy' @('/config/tests/e2e/test_c201_ha.py', 'snapshot')
    Confirm-E2EOutcome 'snapshot'
    Invoke-HaPython "Verify decoded snapshot and initial RTSP stream ($InitialStreamSeconds seconds)" @('/config/tests/e2e/test_c201_ha.py', 'verify', '--seconds', [string]$InitialStreamSeconds)
    Confirm-E2EOutcome 'stream' $InitialStreamSeconds
    if ($SoakSeconds -gt $InitialStreamSeconds) {
        Invoke-HaPython "Verify C201 RTSP stream soak ($SoakSeconds seconds)" @('/config/tests/e2e/test_c201_ha.py', 'stream', '--seconds', [string]$SoakSeconds)
        Confirm-E2EOutcome 'stream' $SoakSeconds
    }

    $null = Invoke-Wslc 'Restart only the isolated C201 gateway' @('restart', $GatewayContainer)
    Invoke-HaPython 'Verify fresh snapshot after gateway restart' @('/config/tests/e2e/test_c201_ha.py', 'snapshot')
    Confirm-E2EOutcome 'snapshot'

    $Succeeded = $true
    Write-Evidence 'PHYSICAL_C201_SNAPSHOT=PASS'
    Write-Evidence "PHYSICAL_C201_STREAM_SECONDS=$SoakSeconds"
    Write-Evidence 'PHYSICAL_C201_RECOVERY=PASS'
    Write-Evidence 'PHYSICAL_C201_PTZ=NOT_EXERCISED'
    Write-Evidence 'RESULT=PASS'
} catch {
    $Failure = $_
    Write-Evidence 'RESULT=FAIL'
    Write-Evidence "FAILURE=$($_.Exception.Message)"
    Write-SanitizedGatewayDiagnostics
} finally {
    if ($KeepStack -and $StackCreated -and ($Succeeded -or $KeepOnFailure)) {
        Write-Evidence 'STACK_KEPT=true'
        Write-Evidence "HA_CONTAINER=$HaContainer"
        Write-Evidence "GATEWAY_CONTAINER=$GatewayContainer"
        Write-Evidence "NETWORK=$Network"
        Write-Evidence "LOCAL_HA_URL=http://127.0.0.1:$HaPort"
        Write-Host "HA_TEST_USERNAME=$HaUsername"
        Write-Host "HA_TEST_PASSWORD=$HaPassword"
        Write-Host "HA_TEST_URL=http://127.0.0.1:$HaPort"
    } elseif ($StackCreated) {
        if ($HaStarted) { $null = Invoke-Wslc 'Remove only this run Home Assistant container' @('remove', '--force', $HaContainer) }
        if ($GatewayStarted) { $null = Invoke-Wslc 'Remove only this run gateway container' @('remove', '--force', $GatewayContainer) }
        $null = Invoke-Wslc 'Remove only this run network' @('network', 'remove', $Network)
    }
    if (-not ($KeepStack -and $StackCreated -and ($Succeeded -or $KeepOnFailure))) {
        $resolvedTestRoot = (Resolve-Path $TestRoot).Path.TrimEnd('\') + '\'
        $resolvedRunRoot = [System.IO.Path]::GetFullPath($RunRoot)
        if ($resolvedRunRoot.StartsWith($resolvedTestRoot, [System.StringComparison]::OrdinalIgnoreCase) -and (Split-Path $resolvedRunRoot -Leaf).StartsWith('run-c201-')) {
            Remove-Item -LiteralPath $resolvedRunRoot -Recurse -Force -ErrorAction SilentlyContinue
        } else {
            Write-Evidence "CLEANUP_SKIPPED_UNSAFE_PATH=$resolvedRunRoot"
        }
    }
    if ($viewer) { $viewer.Token = $null; $viewer.UserId = $null }
    $ApiToken = $null
    $HaPassword = $null
}

Write-Evidence "EVIDENCE_PATH=$Evidence"
if ($Failure) { throw $Failure }
