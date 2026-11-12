[CmdletBinding()]
param(
    [switch]$ExerciseMockPtz,
    [switch]$KeepStack
)

$ErrorActionPreference = 'Continue'
$Wslc = 'C:\Program Files\WSL\wslc.exe'
$HaImage = 'ghcr.io/home-assistant/home-assistant:2026.10.4'
$HaImageDigest = 'sha256:23f4dfbb69ce76315dbc4de7915504f1c2dcecd7ca2648113a493a385972e133'
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$TestRoot = Join-Path $RepoRoot '.test-env'
$EvidenceRoot = Join-Path $TestRoot 'evidence'
$RunId = (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + ([guid]::NewGuid().ToString('N').Substring(0, 8))
$RunRoot = Join-Path $TestRoot ("run-{0}" -f $RunId)
$HaConfig = Join-Path $RunRoot 'ha-config'
$GatewayDir = Join-Path $RunRoot 'gateway'
$Evidence = Join-Path $EvidenceRoot ("test-local-{0}.log" -f $RunId)
$Network = "xlg-e2e-$RunId-net"
$HaContainer = "xlg-e2e-$RunId-ha"
$GatewayContainer = "xlg-e2e-$RunId-gateway"
$MockContainer = "xlg-e2e-$RunId-onvif"
$GatewayImage = "xlg-gateway-e2e:$RunId"
$MockImage = "xlg-onvif-e2e:$RunId"
$OwnedContainers = [System.Collections.Generic.List[string]]::new()
$StackCreated = $false
$Failure = $null

New-Item -ItemType Directory -Force -Path $EvidenceRoot, $RunRoot, $HaConfig, $GatewayDir | Out-Null
Set-Content -Path $Evidence -Value "Xiaomi Local Gateway isolated test run $RunId" -Encoding UTF8

function Write-Evidence([string]$Line) {
    Add-Content -Path $Evidence -Value $Line -Encoding UTF8
    Write-Host $Line
}

function Invoke-Wslc([string]$Phase, [string[]]$Arguments) {
    Write-Evidence "`n=== $Phase ==="
    $output = @(& $Wslc @Arguments 2>&1)
    $code = $LASTEXITCODE
    foreach ($line in $output) {
        $text = [string]$line
        Add-Content -Path $Evidence -Value $text -Encoding UTF8
        Write-Host $text
    }
    Write-Evidence "EXIT_CODE[$Phase]=$code"
    if ($code -ne 0) { throw "$Phase failed with exit code $code" }
    return ,$output
}

function Get-WslcJson([string]$Phase, [string[]]$Arguments) {
    Write-Evidence "`n=== $Phase ==="
    $output = @(& $Wslc @Arguments 2>&1)
    $code = $LASTEXITCODE
    Write-Evidence "EXIT_CODE[$Phase]=$code"
    if ($code -ne 0) { throw "$Phase failed with exit code $code" }
    $json = ($output | ForEach-Object { [string]$_ }) -join "`n"
    return ,(ConvertFrom-Json $json)
}

function Get-TreeManifest([string]$Root, [string]$Prefix = '') {
    $base = (Resolve-Path $Root).Path.TrimEnd('\')
    $files = @(Get-ChildItem -LiteralPath $base -File -Recurse | Where-Object {
        $_.Extension -ne '.pyc' -and $_.FullName -notmatch '[\\/]__pycache__[\\/]'
    } | Sort-Object {
        $_.FullName.Substring($base.Length + 1).Replace('\', '/')
    })
    $lines = foreach ($file in $files) {
        $relative = $file.FullName.Substring($base.Length + 1).Replace('\', '/')
        if ($Prefix) { $relative = "$Prefix/$relative" }
        $hash = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        "$hash  $relative"
    }
    return ,@($lines)
}

function Get-SourceDigest([string]$Root, [string[]]$Patterns) {
    $base = (Resolve-Path $Root).Path.TrimEnd('\')
    $files = @()
    foreach ($pattern in $Patterns) {
        $files += Get-ChildItem -LiteralPath (Join-Path $base $pattern) -File -Recurse | Where-Object {
            $_.Extension -ne '.pyc' -and $_.FullName -notmatch '[\\/]__pycache__[\\/]'
        }
    }
    $lines = foreach ($file in ($files | Sort-Object FullName -Unique)) {
        $relative = $file.FullName.Substring($base.Length + 1).Replace('\', '/')
        $hash = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        "$relative=$hash`n"
    }
    $bytes = [System.Text.Encoding]::UTF8.GetBytes(($lines -join ''))
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try { return ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace('-', '').ToLowerInvariant() }
    finally { $sha.Dispose() }
}

function New-SecretHex([int]$Length = 32) {
    $bytes = New-Object byte[] $Length
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) }
    finally { $rng.Dispose() }
    return ([BitConverter]::ToString($bytes)).Replace('-', '').ToLowerInvariant()
}

function Get-FreeLoopbackPort {
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    try { $listener.Start(); return $listener.LocalEndpoint.Port }
    finally { $listener.Stop() }
}

function Wait-HomeAssistant([int]$Port) {
    $deadline = (Get-Date).AddMinutes(3)
    $url = "http://127.0.0.1:$Port/"
    while ((Get-Date) -lt $deadline) {
        try {
            $response = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 4
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 500) {
                Write-Evidence "HA_READY_URL=$url"
                return
            }
        } catch { Start-Sleep -Seconds 2 }
        Start-Sleep -Seconds 2
    }
    $null = Invoke-Wslc 'HA logs after readiness timeout' @('logs', '--tail', '100', $HaContainer)
    throw 'Fresh Home Assistant did not become reachable within 3 minutes'
}

function Invoke-HaPython([string]$Phase, [string[]]$CommandArgs) {
    return Invoke-Wslc $Phase (@('exec', '--env-file', (Join-Path $RunRoot 'runtime.env'), $HaContainer, 'python3') + $CommandArgs)
}

try {
    if (-not (Test-Path -LiteralPath $Wslc)) { throw "WSLc not found: $Wslc" }

    $haFixture = Join-Path $RepoRoot 'tests/fixtures/home-assistant/configuration.yaml'
    Copy-Item -LiteralPath $haFixture -Destination (Join-Path $HaConfig 'configuration.yaml')
    New-Item -ItemType Directory -Force -Path (Join-Path $HaConfig 'custom_components'), (Join-Path $HaConfig 'tests/e2e') | Out-Null
    $sourceIntegrationRoot = Join-Path $RepoRoot 'custom_components/xiaomi_local_gateway'
    $integrationRoot = Join-Path $HaConfig 'custom_components/xiaomi_local_gateway'
    New-Item -ItemType Directory -Force -Path $integrationRoot | Out-Null
    $sourceFiles = Get-ChildItem -LiteralPath $sourceIntegrationRoot -File -Recurse | Where-Object {
        $_.Extension -ne '.pyc' -and $_.FullName -notmatch '[\\/]__pycache__[\\/]'
    }
    foreach ($sourceFile in $sourceFiles) {
        $relative = $sourceFile.FullName.Substring($sourceIntegrationRoot.Length + 1)
        $destination = Join-Path $integrationRoot $relative
        New-Item -ItemType Directory -Force -Path (Split-Path $destination -Parent) | Out-Null
        Copy-Item -LiteralPath $sourceFile.FullName -Destination $destination
    }
    Copy-Item -LiteralPath (Join-Path $RepoRoot 'tests/test_gateway_client.py') -Destination (Join-Path $HaConfig 'tests/test_gateway_client.py')
    Copy-Item -LiteralPath (Join-Path $RepoRoot 'tests/test_camera_entity.py') -Destination (Join-Path $HaConfig 'tests/test_camera_entity.py')
    Copy-Item -LiteralPath (Join-Path $RepoRoot 'tests/e2e/test_ha.py') -Destination (Join-Path $HaConfig 'tests/e2e/test_ha.py')
    Copy-Item -LiteralPath (Join-Path $RepoRoot 'tests/e2e/test_c201_ha.py') -Destination (Join-Path $HaConfig 'tests/e2e/test_c201_ha.py')
    Copy-Item -LiteralPath (Join-Path $RepoRoot 'tests/e2e/verify_source.py') -Destination (Join-Path $HaConfig 'tests/e2e/verify_source.py')
    $componentManifest = Get-TreeManifest $sourceIntegrationRoot 'custom_components/xiaomi_local_gateway'
    $copiedManifest = Get-TreeManifest $integrationRoot 'custom_components/xiaomi_local_gateway'
    if (($componentManifest -join "`n") -cne ($copiedManifest -join "`n")) { throw 'Copied HA integration differs from current checkout' }
    [System.IO.File]::WriteAllText((Join-Path $HaConfig 'source.sha256'), (($componentManifest -join "`n") + "`n"), [System.Text.Encoding]::ASCII)

    Copy-Item -LiteralPath (Join-Path $RepoRoot 'tests/fixtures/gateway-mock.json') -Destination (Join-Path $GatewayDir 'config.json')
    $gatewayDigest = Get-SourceDigest $RepoRoot @('Dockerfile', 'go.mod', 'go.sum', 'cmd', 'internal')
    $componentDigest = Get-SourceDigest $RepoRoot @('custom_components/xiaomi_local_gateway')
    $testDigest = Get-SourceDigest $RepoRoot @('tests/e2e', 'tests/fixtures', 'tests/mocks')
    Write-Evidence "GATEWAY_SOURCE_SHA256=$gatewayDigest"
    Write-Evidence "HA_INTEGRATION_SOURCE_SHA256=$componentDigest"
    Write-Evidence "E2E_AND_MOCK_SOURCE_SHA256=$testDigest"
    Write-Evidence "HA_IMAGE=$HaImage"

    $haPort = Get-FreeLoopbackPort
    $haUrl = 'http://127.0.0.1:8123'
    $haUsername = "xlg_e2e_$RunId" -replace '-', '_'
    $haPassword = New-SecretHex
    $apiToken = New-SecretHex
    @(
        "HA_URL=$haUrl"
        "HA_USERNAME=$haUsername"
        "HA_PASSWORD=$haPassword"
        "XLG_API_TOKEN=$apiToken"
        'XLG_GATEWAY_URL=http://xlg-gateway:8099'
    ) | Set-Content -Path (Join-Path $RunRoot 'runtime.env') -Encoding ASCII
    "XLG_API_TOKEN=$apiToken" | Set-Content -Path (Join-Path $RunRoot 'gateway.env') -Encoding ASCII

    $null = Invoke-Wslc 'Go race tests in pinned builder stage' @('build', '--no-cache', '--progress', 'plain', '--target', 'go-race', $RepoRoot)
    $null = Invoke-Wslc 'Go vet in pinned builder stage' @('build', '--no-cache', '--progress', 'plain', '--target', 'go-vet', $RepoRoot)

    $null = Invoke-Wslc 'Pull pinned Home Assistant image' @('pull', $HaImage)
    $haVersion = Invoke-Wslc 'Read pinned Home Assistant image version' @('run', '--rm', '--entrypoint', 'python3', $HaImage, '-m', 'homeassistant', '--version')
    $versionText = ($haVersion | ForEach-Object { [string]$_ }) -join ' '
    if ($versionText -notmatch '2026\.10\.4') { throw "Pinned HA image has unexpected version: $versionText" }
    Write-Evidence "HA_VERSION=$versionText"
    $haImageObject = Get-WslcJson 'Verify pinned HA image digest before startup' @('inspect', '--type', 'image', '--format', 'json', $HaImage)
    $expectedHaImageId = $haImageObject[0].Id
    $expectedHaRepoDigest = "ghcr.io/home-assistant/home-assistant@$HaImageDigest"
    if (@($haImageObject[0].RepoDigests) -notcontains $expectedHaRepoDigest) { throw 'Pinned HA image has an unexpected registry digest' }
    $haVersionLabel = $haImageObject[0].Config.Labels.'io.hass.version'
    if ($haVersionLabel -ne '2026.10.4') { throw "Pinned HA image label has unexpected version: $haVersionLabel" }
    Write-Evidence "HA_IMAGE_DIGEST=$HaImageDigest"
    Write-Evidence "HA_IMAGE_ID=$expectedHaImageId"
    Write-Evidence "HA_VERSION_LABEL=$haVersionLabel"

    $null = Invoke-Wslc 'Build current gateway source' @('build', '--progress', 'plain', '-t', $GatewayImage, $RepoRoot)
    $null = Invoke-Wslc 'Build pinned ONVIF and RTSP media mock from checkout' @('build', '--progress', 'plain', '-f', (Join-Path $RepoRoot 'tests/mocks/Dockerfile'), '-t', $MockImage, $RepoRoot)

    $null = Invoke-Wslc 'Create unique test network' @('network', 'create', $Network)
    $StackCreated = $true
    $OwnedContainers.Add($MockContainer)
    $null = Invoke-Wslc 'Start fresh ONVIF and H.264 RTSP mock' @('run', '-d', '--name', $MockContainer, '--network', $Network, '--network-alias', 'xlg-onvif-mock', '--network-alias', 'xlg-rtsp-mock', $MockImage)
    $OwnedContainers.Add($GatewayContainer)
    $null = Invoke-Wslc 'Start gateway built from this checkout' @('run', '-d', '--name', $GatewayContainer, '--network', $Network, '--network-alias', 'xlg-gateway', '--env-file', (Join-Path $RunRoot 'gateway.env'), '-v', "${GatewayDir}:/config:ro", $GatewayImage)
    $OwnedContainers.Add($HaContainer)
    $null = Invoke-Wslc 'Start fresh Home Assistant config' @('run', '-d', '--name', $HaContainer, '--network', $Network, '-p', "127.0.0.1:${haPort}:8123", '-v', "${HaConfig}:/config", $HaImage)
    Wait-HomeAssistant $haPort

    $gatewayImageObject = Get-WslcJson 'Inspect unique gateway image' @('inspect', '--type', 'image', '--format', 'json', $GatewayImage)
    $expectedGatewayImageId = $gatewayImageObject[0].Id
    $gatewayContainerObject = Get-WslcJson 'Inspect fresh gateway container (redacted)' @('inspect', '--type', 'container', '--format', 'json', $GatewayContainer)
    if ($gatewayContainerObject[0].Image -ne $expectedGatewayImageId) { throw 'Gateway container is not running the image built for this run' }
    Write-Evidence "GATEWAY_IMAGE_ID=$expectedGatewayImageId"
    Write-Evidence "GATEWAY_CONTAINER_IMAGE_ID=$($gatewayContainerObject[0].Image)"

    $haContainerObject = Get-WslcJson 'Inspect fresh HA container' @('inspect', '--type', 'container', '--format', 'json', $HaContainer)
    if ($haContainerObject[0].Image -ne $expectedHaImageId) { throw 'HA container is not running the pinned image pulled for this run' }
    Write-Evidence "HA_CONTAINER_IMAGE_ID=$($haContainerObject[0].Image)"
    $null = Invoke-HaPython 'Verify current integration hashes inside HA' @('/config/tests/e2e/verify_source.py')
    $null = Invoke-HaPython 'Home Assistant check_config' @('-m', 'homeassistant', '--script', 'check_config', '--config', '/config')
    $null = Invoke-HaPython 'Python gateway client tests' @('-m', 'unittest', 'discover', '-s', '/config/tests', '-p', 'test_gateway_client.py', '-v')
    $null = Invoke-HaPython 'HA camera stream capability follows coordinator updates' @('/config/tests/test_camera_entity.py')
    $null = Invoke-HaPython 'Fresh HA onboarding and integration config flow' @('/config/tests/e2e/test_ha.py', 'setup')
    $null = Invoke-HaPython 'Verify HA camera and PTZ entities' @('/config/tests/e2e/test_ha.py', 'verify-entities')
    $null = Invoke-HaPython 'Verify C201 snapshot through HA camera proxy and live HA HLS stream' @('/config/tests/e2e/test_c201_ha.py', 'verify', '--seconds', '12')
    $null = Invoke-HaPython 'Verify direct gateway RTSP diagnostic against mock source' @('/config/tests/e2e/test_c201_ha.py', 'direct-rtsp', '--seconds', '5')
    $rtspLogs = Invoke-Wslc 'Read isolated mock RTSP evidence' @('logs', $MockContainer)
    $rtspRows = @($rtspLogs | ForEach-Object { [string]$_ } | Where-Object { $_ -match 'MOCK_RTSP action=' })
    $rtspActions = @($rtspRows | ForEach-Object { if ($_ -match 'MOCK_RTSP action=([a-z]+)') { $Matches[1] } })
    if ($rtspActions -notcontains 'describe' -or $rtspActions -notcontains 'setup' -or $rtspActions -notcontains 'play') {
        throw 'Expected the HA snapshot and live HLS check to reach the isolated RTSP mock through DESCRIBE, SETUP, and PLAY'
    }
    foreach ($row in $rtspRows) { Write-Evidence "MOCK_EVIDENCE $row" }

    if ($ExerciseMockPtz) {
        $null = Invoke-HaPython 'Exercise four PTZ directions against isolated mock' @('/config/tests/e2e/test_ha.py', 'press')
        $mockLogs = Invoke-Wslc 'Read isolated mock PTZ evidence' @('logs', $MockContainer)
        $mockRows = @($mockLogs | ForEach-Object { [string]$_ } | Where-Object { $_ -match 'MOCK_ONVIF ' })
        if ($mockRows.Count -ne 4) { throw "Expected four isolated ONVIF mock requests; found $($mockRows.Count)" }
        foreach ($row in $mockRows) { Write-Evidence "MOCK_EVIDENCE $row" }
    }

    $null = Invoke-Wslc 'Stop only this run gateway for outage check' @('stop', $GatewayContainer)
    $null = Invoke-HaPython 'Verify cameras become unavailable' @('/config/tests/e2e/test_ha.py', 'wait-unavailable')
    $null = Invoke-Wslc 'Restart only this run gateway' @('start', $GatewayContainer)
    $null = Invoke-HaPython 'Verify cameras recover after gateway restart' @('/config/tests/e2e/test_ha.py', 'wait-available')

    $finalComponentDigest = Get-SourceDigest $RepoRoot @('custom_components/xiaomi_local_gateway')
    $finalGatewayDigest = Get-SourceDigest $RepoRoot @('Dockerfile', 'go.mod', 'go.sum', 'cmd', 'internal')
    $finalTestDigest = Get-SourceDigest $RepoRoot @('tests/e2e', 'tests/fixtures', 'tests/mocks')
    if ($finalComponentDigest -ne $componentDigest -or $finalGatewayDigest -ne $gatewayDigest -or $finalTestDigest -ne $testDigest) {
        throw 'Current source changed while this test run was in progress'
    }
    Write-Evidence 'RESULT=PASS'
    Write-Evidence 'C201_VIDEO=UNVERIFIED'
} catch {
    $Failure = $_
    Write-Evidence "RESULT=FAIL"
    Write-Evidence "FAILURE=$($_.Exception.Message)"
} finally {
    if ($KeepStack -and $StackCreated) {
        Write-Evidence "STACK_KEPT=true"
        Write-Evidence "HA_CONTAINER=$HaContainer"
        Write-Evidence "GATEWAY_CONTAINER=$GatewayContainer"
        Write-Evidence "ONVIF_CONTAINER=$MockContainer"
        Write-Evidence "NETWORK=$Network"
        Write-Evidence "LOCAL_HA_URL=http://127.0.0.1:$haPort"
        Write-Evidence "LOCAL_CREDENTIALS_PATH=$(Join-Path $RunRoot 'runtime.env')"
        Write-Evidence "LOCAL_GATEWAY_CREDENTIALS_PATH=$(Join-Path $RunRoot 'gateway.env')"
    } else {
        if ($OwnedContainers.Count -gt 0) {
            $null = Invoke-Wslc 'Cleanup containers created by this run' (@('remove', '--force') + $OwnedContainers.ToArray())
        }
        if ($StackCreated) { $null = Invoke-Wslc 'Cleanup network created by this run' @('network', 'remove', $Network) }
        $resolvedTestRoot = (Resolve-Path $TestRoot).Path.TrimEnd('\') + '\'
        $resolvedRunRoot = [System.IO.Path]::GetFullPath($RunRoot)
        if ($resolvedRunRoot.StartsWith($resolvedTestRoot, [System.StringComparison]::OrdinalIgnoreCase) -and (Split-Path $resolvedRunRoot -Leaf).StartsWith('run-')) {
            Remove-Item -LiteralPath $resolvedRunRoot -Recurse -Force -ErrorAction SilentlyContinue
        } else {
            Write-Evidence "CLEANUP_SKIPPED_UNSAFE_PATH=$resolvedRunRoot"
        }
    }
}

Write-Evidence "EVIDENCE_PATH=$Evidence"
if ($Failure) { throw $Failure }
