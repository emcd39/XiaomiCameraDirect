# Xiaomi C201 E1 — finální předání

## Stav a důkazy

- Uživatel nahlásil novou IP fyzické C201: **192.168.2.124**. Dosažitelnost a identita zařízení nejsou nezávisle ověřené. Dřívější 192.168.3.119 je pouze historická.
- PID **38416 je historický**. Read-only kontrola 2026-11-10 20:15:59 +01:00 skončila: CIM **UNVERIFIED / CimException**, TCP listenery **UNVERIFIED / MethodInvocationException**, ActiveStore firewall **UNVERIFIED / MethodInvocationException**. Omezení přístupu nebyla obcházena.
- Připravená gateway: C:\DEV\xiaomi-local-gateway\.test-env\builds\managed-ptz-finalization-20261109-f\gateway.exe. SHA-256: 591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0
- Připravený managed PTZ helper: C:\DEV\xiaomi-local-gateway\.test-env\builds\managed-ptz-handoff\managed-ptz-http-probe.exe. SHA-256: 6CD815C46BCC636438D9605139948A6A93208942D85C2A31025B8B57C4C38D5D
- Offline testy helperu prošly: go test ./.test-env/c201-pilot/win-gateway/managed-ptz-handoff -count=1 -v, exit 0. Cizí host, neúspěšná identita, historický/nejasný session log a nestabilní baseline znamenaly nula PTZ POSTů. Stabilní syntetická baseline znamenala právě jeden mock POST; timeout znamenal jeden pokus a nula retry.
- PowerShell parser prošel pro runtime verifier, launcher a WSL firewall helper. Spuštění runtime verifieru přes powershell -File skončilo v omezeném prostředí AuthorizationManager; skutečný proces, listenery, session a firewall zůstávají neověřené.
- V tomto předání se nic nenasazuje: nebyl zastaven ani spuštěn proces, nebyl změněn firewall, neotevřelo se spojení s kamerou a nebyl odeslán PTZ.

Starší soubor C201_E1_PREDANI.md je návrh s neúplnými bloky; tento dokument je jeho autoritativní náhradou. Všechny příkazy níže jsou jen pro budoucí samostatně schválený běh. Žádný blok nyní nespouštět.

## 1. Čerstvý read-only preflight

Provádět později v běžném Windows PowerShellu pod stejným Windows účtem jako cílová instance. Nepoužívat historický PID. Pokud CIM, sockety, profil, log nebo firewall vrátí chybu/nejednoznačný výsledek, skončit před odstávkou. CIM neukazuje aktuální working directory ani prostředí procesu; tyto údaje musí být doloženy důvěryhodným launcherem/startovním záznamem. Bez něj je cíl neověřený.

    $ErrorActionPreference = 'Stop'
    $script:C201RunbookState = 'Preflight'
    $script:C201RunbookStatePath = $null
    $script:C201RollbackCompleted = $false
    $script:C201OriginalStopConfirmed = $false
    $script:C201RecoveryOutcome = 'not_started'
    $script:C201RestartedOriginalSnapshot = $null
    $script:C201RestartedOriginalProcess = $null
    $script:C201ActiveLaunchRole = $null
    $script:C201NewProcessHandle = $null
    $script:C201NewProcessBinding = $null
    $script:C201NewProcessReceipt = $null
    $script:C201NewLaunchAttemptId = $null
    $script:C201NewLaunchAttemptUtc = $null
    function Get-C201RestartedProcessOutcome {
        if ($script:C201RestartedOriginalProcess) {
            try {
                if ($script:C201RestartedOriginalProcess.PSObject.Methods['Refresh']) { $script:C201RestartedOriginalProcess.Refresh() }
                if ($script:C201RestartedOriginalProcess.HasExited) { return 'original_gateway=stopped_after_restart_attempt' }
                return 'original_gateway=running_process_handle_confirmed; readiness=failed'
            } catch { return 'original_gateway=process_handle_state_unverified_after_restart' }
        }
        if (-not $script:C201RestartedOriginalSnapshot) { return 'original_gateway=not_restarted' }
        try {
            $state = Get-C201OriginalProcessState $script:C201RestartedOriginalSnapshot
            if ($state -eq 'running') { return 'original_gateway=running_identity_verified; readiness=failed' }
            return 'original_gateway=stopped_after_restart_attempt'
        } catch { return 'original_gateway=identity_or_state_unverified_after_restart' }
    }
    function Get-C201FfmpegVersionLine([string]$Path) {
        $lines = @(& $Path -version 2>$null)
        $exit = $LASTEXITCODE
        if ($exit -ne 0 -or $lines.Count -eq 0 -or [string]$lines[0] -notmatch '^ffmpeg version\s') { throw 'Standalone FFmpeg executable check failed; STOP before downtime.' }
        return [string]$lines[0]
    }
    function Assert-C201ImageFfmpegPath([string]$Path) {
        if ([string]::IsNullOrWhiteSpace($Path) -or -not [IO.Path]::IsPathRooted($Path) -or -not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw 'Standalone FFmpeg path is missing or invalid; STOP before downtime.' }
        $resolved = [IO.Path]::GetFullPath($Path)
        $versionLine = Get-C201FfmpegVersionLine $resolved
        if ($versionLine -notmatch '^ffmpeg version\s') { throw 'Standalone FFmpeg version output is invalid; STOP before downtime.' }
        $versionLine = $null
        return $resolved
    }
    function Invoke-C201FailureHandler([string]$Reason) {
        $safeReason = ([string]$Reason -replace '[\r\n]+',' ')
        if ($script:C201RunbookState -in @('Preflight','BackupReady') -and -not $script:C201OriginalStopConfirmed) {
            return [pscustomobject]@{ state=$script:C201RunbookState; exit_code=1; recovery='not_started_original_left_running'; reason=$safeReason }
        }
        try {
            $result = Invoke-C201Rollback
            if ($result.action -eq 'stop_without_restart') { return [pscustomobject]@{ state='OriginalRunning'; exit_code=1; recovery=$script:C201RecoveryOutcome; reason=$safeReason } }
            if ($result.state -eq 'RolledBack' -and $script:C201RollbackCompleted) { return [pscustomobject]@{ state='RolledBack'; exit_code=1; recovery=$script:C201RecoveryOutcome; reason=$safeReason } }
            throw 'Rollback returned without a verified completion marker.'
        } catch {
            $recoveryReason = ([string]$_.Exception.Message -replace '[\r\n]+',' ')
            if ($script:C201RestartedOriginalSnapshot) { $script:C201RecoveryOutcome = Get-C201RestartedProcessOutcome }
            try { Set-C201RunbookState 'RecoveryFailed' } catch {}
            return [pscustomobject]@{ state='RecoveryFailed'; exit_code=20; recovery=$script:C201RecoveryOutcome + '; ' + $recoveryReason; reason=$safeReason }
        }
    }
    trap {
        $reason = ([string]$_.Exception.Message -replace '[\r\n]+',' ')
        try {
            $failureResult = Invoke-C201FailureHandler $reason
            if ($failureResult.state -eq 'RecoveryFailed') {
                [Console]::Error.WriteLine(('C201_RUNBOOK=FAIL state=RecoveryFailed exit_code=20 actual=' + $failureResult.recovery + ' cause=' + $failureResult.reason))
                exit 20
            }
            [Console]::Error.WriteLine(('C201_RUNBOOK=FAIL state=' + $failureResult.state + ' exit_code=' + $failureResult.exit_code + ' recovery=' + $failureResult.recovery + ' cause=' + $failureResult.reason))
            exit [int]$failureResult.exit_code
        } catch {
            $recoveryReason = ([string]$_.Exception.Message -replace '[\r\n]+',' ')
            try { Set-C201RunbookState 'RecoveryFailed' } catch {}
            [Console]::Error.WriteLine(('C201_RUNBOOK=FAIL state=RecoveryFailed exit_code=20 actual=' + $script:C201RecoveryOutcome + ' cause=' + $reason + ' recovery=' + $recoveryReason))
            exit 20
        }
    }
    $NewExe = 'C:\DEV\xiaomi-local-gateway\.test-env\builds\managed-ptz-finalization-20261109-f\gateway.exe'
    $ExpectedNewHash = '591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0'
    $CameraAddress = '192.168.2.124'
    $TargetPid = [int](Read-Host 'PID zjištěný právě teď')
    $ClientAddress = Read-Host 'Skutečná IPv4/IPv6 adresa RTSP klienta'
    $VerifiedWorkingDirectory = Read-Host 'Working directory doložený startovním záznamem'
    if (-not (Test-Path -LiteralPath $NewExe -PathType Leaf)) { throw 'Nová gateway chybí; STOP.' }
    $newHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $NewExe -ErrorAction Stop).Hash
    if ($newHash -ne $ExpectedNewHash) { throw 'SHA-256 nové gateway nesouhlasí; STOP.' }
    if ($TargetPid -le 0 -or -not $ClientAddress -or -not $VerifiedWorkingDirectory) { throw 'Chybí čerstvý vstup; STOP.' }
    $allProcesses = @(Get-CimInstance Win32_Process -ErrorAction Stop)
    $rows = @($allProcesses | Where-Object { $_.ProcessId -eq $TargetPid })
    if ($rows.Count -ne 1 -or -not $rows[0].ExecutablePath -or -not $rows[0].CommandLine) { throw 'PID nebo úplná identita není čitelná; STOP.' }
    $wmi = $rows[0]
    $TargetExe = [IO.Path]::GetFullPath($wmi.ExecutablePath)
    $owner = Invoke-CimMethod -InputObject $wmi -MethodName GetOwner -ErrorAction Stop
    if ($owner.ReturnValue -ne 0) { throw 'Vlastník procesu není čitelný; STOP.' }
    $OwnerSid = ([Security.Principal.NTAccount]::new($owner.Domain,$owner.User)).Translate([Security.Principal.SecurityIdentifier]).Value
    $CurrentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    if ($OwnerSid -ne $CurrentSid) { throw 'Účet Windows/DPAPI se neshoduje; STOP.' }
    $TargetHandle = Get-C201ProcessHandle $TargetPid
    if ($TargetHandle.HasExited -or [IO.Path]::GetFullPath($TargetHandle.MainModule.FileName) -ne $TargetExe) { throw 'PID se změnil; STOP.' }
    $StartUtc = $TargetHandle.StartTime.ToUniversalTime()
    $listenMatch = [regex]::Match($wmi.CommandLine,'(?i)(?:^|\s)-listen\s+(?:"([^"]+)"|(\S+))')
    $dataMatch = [regex]::Match($wmi.CommandLine,'(?i)(?:^|\s)-data-dir\s+(?:"([^"]+)"|(\S+))')
    if (-not $listenMatch.Success -or -not $dataMatch.Success -or $wmi.CommandLine -notmatch '(?i)(?:^|\s)-managed(?:\s|$)') { throw 'Neznámé argumenty; STOP.' }
    $ApiListen = if ($listenMatch.Groups[1].Success) { $listenMatch.Groups[1].Value } else { $listenMatch.Groups[2].Value }
    $rawDataDir = if ($dataMatch.Groups[1].Success) { $dataMatch.Groups[1].Value } else { $dataMatch.Groups[2].Value }
    if (-not [IO.Path]::IsPathRooted($rawDataDir) -or $ApiListen -notmatch '^127\.0\.0\.1:(?<p>\d+)$') { throw 'Relativní data-dir nebo API mimo loopback; STOP.' }
    $DataDir = [IO.Path]::GetFullPath($rawDataDir)
    $ApiPort = [int]$Matches.p
    $quotedExe = '"' + $TargetExe + '" '
    if ($wmi.CommandLine.StartsWith($quotedExe,[StringComparison]::OrdinalIgnoreCase)) { $argumentTail = $wmi.CommandLine.Substring($quotedExe.Length) }
    elseif ($wmi.CommandLine.StartsWith(($TargetExe + ' '),[StringComparison]::OrdinalIgnoreCase)) { $argumentTail = $wmi.CommandLine.Substring($TargetExe.Length + 1) }
    else { throw 'CommandLine executable prefix is ambiguous; STOP.' }
    $expectedTail = '-managed -listen ' + $ApiListen + ' -data-dir "' + $DataDir + '"'
    if ($argumentTail -cne $expectedTail) { throw 'Unexpected gateway arguments; STOP.' }
    $RunRoot = Split-Path -Parent $DataDir
    $PrivateDir = Join-Path $RunRoot 'private'
    $LaunchFile = Join-Path $RunRoot 'firewall-target.json'
    $TokenFile = Join-Path $PrivateDir 'api-token.txt'
    $LogFile = Join-Path $PrivateDir 'gateway.stderr.log'
    $launch = Get-Content -Raw -LiteralPath $LaunchFile -ErrorAction Stop | ConvertFrom-Json
    if ($launch.gateway_pid -ne $TargetPid -or [IO.Path]::GetFullPath([string]$launch.gateway_executable) -ne $TargetExe -or $launch.camera_address -ne $CameraAddress -or $launch.api_port -ne $ApiPort) { throw 'Startovní manifest neodpovídá procesu nebo aktuální IP; STOP.' }
    if ([IO.Path]::GetFullPath($VerifiedWorkingDirectory) -ne [IO.Path]::GetFullPath($RunRoot)) { throw 'Working directory není doložen launcherem; STOP.' }
    $RtspPort = [int]$launch.rtsp_port
    $RtspAdvertiseHost = [string]$launch.local_address
    if ([string]::IsNullOrWhiteSpace($RtspAdvertiseHost)) { throw 'RTSP advertise host missing; STOP.' }
    $VerifiedPath = Read-Host 'Přesný PATH procesu gateway z důvěryhodného startovního zdroje'
    if ([string]::IsNullOrWhiteSpace($VerifiedPath)) { throw 'Původní PATH neověřen; STOP.' }
    $GatewayFfmpegSetting = Read-Host 'Původní XLG_FFMPEG_PATH; zadej ABSENT pouze pokud to potvrzuje startovní záznam'
    if ($GatewayFfmpegSetting -eq 'ABSENT') { $GatewayFfmpegEnvPresent = $false; $GatewayFfmpegEnvValue = $null }
    elseif ([string]::IsNullOrWhiteSpace($GatewayFfmpegSetting) -or -not [IO.Path]::IsPathRooted($GatewayFfmpegSetting) -or -not (Test-Path -LiteralPath $GatewayFfmpegSetting -PathType Leaf)) { throw 'Původní XLG_FFMPEG_PATH není doložen nebo soubor chybí; STOP.' }
    else { $GatewayFfmpegEnvPresent = $true; $GatewayFfmpegEnvValue = $GatewayFfmpegSetting }
    $VerifiedFfmpegPath = Assert-C201ImageFfmpegPath (Read-Host 'Samostatná ověřovací cesta k ffmpeg.exe pro RTSP kontrolu')
    $apiListeners = @(Get-NetTCPConnection -State Listen -LocalPort $ApiPort -ErrorAction Stop)
    $rtspListeners = @(Get-NetTCPConnection -State Listen -LocalPort $RtspPort -ErrorAction Stop)
    if ($apiListeners.Count -ne 1 -or $apiListeners[0].OwningProcess -ne $TargetPid -or $apiListeners[0].LocalAddress -ne '127.0.0.1') { throw 'API listener není jednoznačně svázán s cílem; STOP.' }
    if ($rtspListeners.Count -ne 1 -or $rtspListeners[0].OwningProcess -ne $TargetPid -or $rtspListeners[0].LocalAddress -ne '0.0.0.0') { throw 'RTSP listener není jednoznačně svázán s cílem; STOP.' }
    if (-not (Test-Path -LiteralPath $TokenFile -PathType Leaf) -or -not (Test-Path -LiteralPath $LogFile -PathType Leaf)) { throw 'Chybí token/log této instance; STOP.' }
    $TargetSnapshot = [pscustomobject]@{ Pid=$TargetPid; OwnerSid=$OwnerSid; Executable=$TargetExe; CommandLine=[string]$wmi.CommandLine; StartUtc=$StartUtc; ApiListen=$ApiListen; ApiPort=$ApiPort; RtspPort=$RtspPort; DataDir=$DataDir; RunRoot=$RunRoot; PrivateDir=$PrivateDir }
    [pscustomobject]@{ pid=$TargetPid; owner_sid=$OwnerSid; executable=$TargetExe; start_utc=$StartUtc.ToString('o'); api_listen=$ApiListen; rtsp_port=$RtspPort; rtsp_advertise_host=$RtspAdvertiseHost; data_dir=$DataDir; working_directory=$RunRoot } | Format-List

## 2. Read-only kontrola session a firewallu

Spusť připravený verifier jako samostatný proces; exit jiný než 0 znamená STOP. `pre-inventory` ověřuje manifest, účet/DPAPI, inventář mladší než 20 s, právě jednu `session_open cs2+tcp`, `first_media`, `rtsp_description`, právě jednu aktivní TCP cestu k C201 a nulový počet dalších gateway session. Použij ho read-only před odstávkou. Nově podporovaný `post-start` je jen místní read-only gate: ověří přesnou identitu procesu, API listener, profil/tokenové soubory, ostatní gateway a TCP stav C201 a vrátí `state=empty|starting|ready`. Neodesílá HTTP ani Bearer. Právě `GET /v2/cameras` může zavolat `Streams()` a založit session, proto ho před start gate nevolej.

    $Verifier = 'C:\DEV\xiaomi-local-gateway\.test-env\c201-pilot\win-gateway\managed-ptz-handoff\verify-managed-ptz-runtime.ps1'
    $PowerShellExe = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    $StartUtcText = $StartUtc.ToString('o')
    & $PowerShellExe -NoProfile -File $Verifier -ExpectedPid $TargetPid -ExpectedExe $TargetExe -ExpectedStartUtc $StartUtcText -ExpectedDataDir $DataDir -ExpectedApiPort $ApiPort -CameraAddress $CameraAddress -Stage pre-inventory
    $verifyExit = $LASTEXITCODE
    if ($verifyExit -ne 0) { throw 'Runtime/session verifier neprošel; STOP před odstávkou.' }

Pro TCP RTSP aktuálního portu musí být doloženo efektivní ActiveStore pravidlo pro novou executable cestu a skutečného klienta. Následující pouze vypisuje kandidátní pravidla; operátor musí zkontrolovat aktivní profil, DefaultInboundAction, portové rozsahy, remote/local scope, Program, Service/Interface/Security filtry, GPO precedence a odpovídající bloky. Bez jednoznačného Allow pro novou cestu/klienta/profil a bez odpovídajícího Block je FIREWALL=UNVERIFIED a STOP. Pravidla se nemění.

    $activeNetwork = @(Get-NetConnectionProfile -ErrorAction Stop)
    $profiles = @(Get-NetFirewallProfile -PolicyStore ActiveStore -ErrorAction Stop)
    $rules = @(Get-NetFirewallRule -PolicyStore ActiveStore -Direction Inbound -Enabled True -ErrorAction Stop)
    function Test-PortMayMatch([object[]]$Spec,[int]$Wanted) {
        foreach ($value in $Spec) {
            foreach ($part in ([string]$value -split ',')) {
                $s = $part.Trim()
                if ($s -eq 'Any') { return $true }
                if ($s -match '^\d+$' -and [int]$s -eq $Wanted) { return $true }
                if ($s -match '^(?<a>\d+)-(?<b>\d+)$' -and $Wanted -ge [int]$Matches.a -and $Wanted -le [int]$Matches.b) { return $true }
                if ($s -notmatch '^\d+$|^\d+-\d+$') { return $true }
            }
        }
        return $false
    }
    $candidates = foreach ($rule in $rules) {
        $port = $rule | Get-NetFirewallPortFilter -ErrorAction Stop
        $app = $rule | Get-NetFirewallApplicationFilter -ErrorAction Stop
        $address = $rule | Get-NetFirewallAddressFilter -ErrorAction Stop
        $service = $rule | Get-NetFirewallServiceFilter -ErrorAction Stop
        $interface = $rule | Get-NetFirewallInterfaceTypeFilter -ErrorAction Stop
        $security = $rule | Get-NetFirewallSecurityFilter -ErrorAction Stop
        $protocol = [string]$port.Protocol
        $program = [string]$app.Program
        if ($protocol -in @('TCP','6','Any') -and (Test-PortMayMatch @($port.LocalPort) $RtspPort) -and ($program -eq 'Any' -or [string]::Equals($program,$NewExe,[StringComparison]::OrdinalIgnoreCase))) {
            [pscustomobject]@{ name=$rule.Name; action=[string]$rule.Action; profile=[string]$rule.Profile; program=$program; protocol=$protocol; local_port=(@($port.LocalPort) -join ','); local_address=(@($address.LocalAddress) -join ','); remote_address=(@($address.RemoteAddress) -join ','); service=[string]$service.Service; interface_type=(@($interface.InterfaceType) -join ','); authentication=[string]$security.Authentication; encryption=[string]$security.Encryption }
        }
    }
    [pscustomobject]@{ active_network=$activeNetwork | Select-Object InterfaceAlias,NetworkCategory,IPv4Connectivity; firewall_profiles=$profiles | Select-Object Name,Enabled,DefaultInboundAction,AllowLocalFirewallRules; rtsp_client=$ClientAddress; new_executable=$NewExe; rtsp_port=$RtspPort; candidate_rules=@($candidates) } | ConvertTo-Json -Depth 6
    # Ručně potvrdit aktivní profil, skutečnou klientskou IP, novou cestu, explicitní Allow a žádný odpovídající Block.
    # Nečitelný filtr, neověřený rozsah, LocalSubnet bez potvrzení nebo nejasnost = STOP.

## 3. Backup a výměna — pouze po dalším schválení

Backup root musí být soukromý, pod private adresářem ověřené instance. Ověřit jeho ACL proti private adresáři. Statické soubory zazálohovat a hash porovnat před odstávkou; konzistentní managed-state.json a otevřené logy kopírovat až po zastavení ověřeného procesu.

    $ConfigFile = Join-Path $env:APPDATA 'XiaomiViewer/config.json'
    $stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ')
    $BackupRoot = Join-Path $PrivateDir ('e1-backup-' + $stamp)
    New-Item -ItemType Directory -Path $BackupRoot -ErrorAction Stop | Out-Null
    $script:C201RunbookStatePath = Join-Path $BackupRoot 'runbook-state.json'
    [IO.File]::WriteAllText($script:C201RunbookStatePath,([pscustomobject]@{ state='Preflight'; updated_utc=[DateTime]::UtcNow.ToString('o') } | ConvertTo-Json -Compress),[Text.UTF8Encoding]::new($false))
    $privateAcl = Get-Acl -LiteralPath $PrivateDir -ErrorAction Stop
    Set-Acl -LiteralPath $BackupRoot -AclObject $privateAcl
    $privateSddl = $privateAcl.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::All)
    if ((Get-Acl -LiteralPath $BackupRoot).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::All) -ne $privateSddl) { throw 'Backup ACL mismatch; STOP.' }
    function Save-C201BackupMetadata([string]$Path,[string]$AclSourcePath) {
        if (-not (Test-Path -LiteralPath $Path -PathType Leaf) -or -not (Test-Path -LiteralPath $AclSourcePath -PathType Leaf)) { throw 'Backup metadata source is missing; STOP.' }
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $Path -ErrorAction Stop).Hash
        $sddl = (Get-Acl -LiteralPath $AclSourcePath -ErrorAction Stop).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::All)
        if ($hash -notmatch '^[A-Fa-f0-9]{64}$' -or [string]::IsNullOrWhiteSpace($sddl)) { throw 'Backup hash or ACL metadata is unreadable; STOP.' }
        [IO.File]::WriteAllText(($Path + '.sha256'),$hash,[Text.Encoding]::ASCII)
        [IO.File]::WriteAllText(($Path + '.acl.sddl'),$sddl,[Text.UTF8Encoding]::new($false))
    }
    function Assert-C201BackupArtifact([string]$Name) {
        $path = Join-Path $BackupRoot $Name
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw ('Required backup missing: ' + $Name) }
        $hashPath = $path + '.sha256'
        $aclPath = $path + '.acl.sddl'
        if (-not (Test-Path -LiteralPath $hashPath -PathType Leaf) -or -not (Test-Path -LiteralPath $aclPath -PathType Leaf)) { throw ('Backup hash or ACL record missing: ' + $Name) }
        $expectedHash = [IO.File]::ReadAllText($hashPath).Trim()
        $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $path -ErrorAction Stop).Hash
        if ($expectedHash -notmatch '^[A-Fa-f0-9]{64}$' -or $actualHash -ne $expectedHash) { throw ('Backup hash invalid or mismatched: ' + $Name) }
        $sddl = [IO.File]::ReadAllText($aclPath)
        if ([string]::IsNullOrWhiteSpace($sddl)) { throw ('Backup ACL record empty: ' + $Name) }
        $parsedAcl = [Security.AccessControl.FileSecurity]::new()
        $parsedAcl.SetSecurityDescriptorSddlForm($sddl,[Security.AccessControl.AccessControlSections]::All)
        if ([string]::IsNullOrWhiteSpace($parsedAcl.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::All))) { throw ('Backup ACL record invalid: ' + $Name) }
        $null = Get-Acl -LiteralPath $path -ErrorAction Stop
        return [pscustomobject]@{ Path=$path; Hash=$actualHash; AclSddl=$sddl }
    }
    function Assert-C201RollbackBackupSet($FailurePlan,$TargetSnapshot,[bool]$StatePresent,[string]$StateHash,[string]$StateSddl) {
        $binary = Assert-C201BackupArtifact 'original-gateway.exe'
        $binaryHashRecord = Join-Path $BackupRoot 'original-gateway.sha256'
        if (-not (Test-Path -LiteralPath $binaryHashRecord -PathType Leaf)) { throw 'Original executable checksum record is missing; no restore files were changed.' }
        $recordedBinaryHash = [IO.File]::ReadAllText($binaryHashRecord).Trim()
        if ($recordedBinaryHash -notmatch '^[A-Fa-f0-9]{64}$' -or $recordedBinaryHash -ne $binary.Hash) { throw 'Original executable checksum record is invalid; no restore files were changed.' }
        if ([IO.Path]::GetFullPath($TargetExe) -ne [IO.Path]::GetFullPath([string]$TargetSnapshot.Executable)) { throw 'Original executable destination is not uniquely identified; no restore files were changed.' }
        $targetArtifact = Assert-C201BackupArtifact 'target-process.json'
        $targetRecord = Get-Content -Raw -LiteralPath $targetArtifact.Path -ErrorAction Stop | ConvertFrom-Json
        $expectedTargetArgs = '-managed -listen ' + $ApiListen + ' -data-dir "' + $DataDir + '"'
        $expectedTargetCommand = '"' + [IO.Path]::GetFullPath($TargetExe) + '" ' + $expectedTargetArgs
        $expectedTargetStart = if ($TargetSnapshot.StartUtc -is [DateTime]) { $TargetSnapshot.StartUtc.ToUniversalTime() } else { [DateTime]::Parse([string]$TargetSnapshot.StartUtc).ToUniversalTime() }
        if ([int]$targetRecord.pid -ne [int]$TargetSnapshot.Pid -or [string]$targetRecord.owner_sid -cne [string]$TargetSnapshot.OwnerSid -or [IO.Path]::GetFullPath([string]$targetRecord.executable) -ne [IO.Path]::GetFullPath($TargetExe) -or [string]$targetRecord.command_line -cne $expectedTargetCommand -or [IO.Path]::GetFullPath([string]$targetRecord.data_dir) -ne [IO.Path]::GetFullPath($DataDir) -or [DateTime]::Parse([string]$targetRecord.start_utc).ToUniversalTime().Ticks -ne $expectedTargetStart.Ticks) { throw 'Original process backup identity is inconsistent; no restore files were changed.' }
        $preStopArtifact = Assert-C201BackupArtifact 'pre-stop-state.json'
        $preStop = Get-Content -Raw -LiteralPath $preStopArtifact.Path -ErrorAction Stop | ConvertFrom-Json
        if ([bool]$preStop.present -ne $StatePresent -or ([string]$preStop.hash) -cne [string]$StateHash -or ([string]$preStop.acl_sddl) -cne [string]$StateSddl) { throw 'Pre-stop persistent-state identity is inconsistent; no restore files were changed.' }
        if ($StatePresent) {
            if ([string]$StateHash -notmatch '^[A-Fa-f0-9]{64}$' -or [string]::IsNullOrWhiteSpace([string]$StateSddl)) { throw 'Pre-stop persistent-state hash or ACL is invalid; no restore files were changed.' }
            $stateAcl = [Security.AccessControl.FileSecurity]::new()
            $stateAcl.SetSecurityDescriptorSddlForm([string]$StateSddl,[Security.AccessControl.AccessControlSections]::All)
        }
        foreach ($name in @('api-token.txt','firewall-target.json','XiaomiViewer-config.dpapi.json')) { $null = Assert-C201BackupArtifact $name }
        foreach ($name in @('gateway.stderr.log','gateway.stdout.log','gateway.pid')) {
            if (Test-Path -LiteralPath (Join-Path $BackupRoot $name) -PathType Leaf) { $null = Assert-C201BackupArtifact $name }
        }
        if ($FailurePlan.restore_state) {
            $stateBackup = Join-Path $BackupRoot 'managed-state.json'
            $absentMarker = Join-Path $BackupRoot 'managed-state.absent'
            if ($StatePresent) {
                if (Test-Path -LiteralPath $absentMarker) { throw 'Persistent-state backup is ambiguous; no restore files were changed.' }
                $savedState = Assert-C201BackupArtifact 'managed-state.json'
            } else {
                if (-not (Test-Path -LiteralPath $absentMarker -PathType Leaf) -or [IO.File]::ReadAllText($absentMarker).Trim() -cne 'absent' -or (Test-Path -LiteralPath $stateBackup)) { throw 'Persistent-state absence backup is missing or ambiguous; no restore files were changed.' }
            }
        }
        return $true
    }
    function Save-BackupFile([string]$Source,[string]$Name) {
        if (-not (Test-Path -LiteralPath $Source -PathType Leaf)) { throw ('Backup source missing: ' + $Name) }
        $destination = Join-Path $BackupRoot $Name
        Copy-Item -LiteralPath $Source -Destination $destination -ErrorAction Stop
        Save-C201BackupMetadata $destination $Source
        if ((Get-FileHash -Algorithm SHA256 -LiteralPath $Source).Hash -ne (Get-FileHash -Algorithm SHA256 -LiteralPath $destination).Hash) { throw ('Backup hash mismatch: ' + $Name) }
    }
    function Get-C201ProcessHandle([int]$ProcessId) { return [Diagnostics.Process]::GetProcessById($ProcessId) }
    Save-BackupFile $TargetExe 'original-gateway.exe'
    Save-BackupFile $TokenFile 'api-token.txt'
    Save-BackupFile $LaunchFile 'firewall-target.json'
    Save-BackupFile $ConfigFile 'XiaomiViewer-config.dpapi.json'
    $OriginalHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $TargetExe).Hash
    if ((Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $BackupRoot 'original-gateway.exe')).Hash -ne $OriginalHash) { throw 'Original executable backup failed; STOP.' }
    [IO.File]::WriteAllText((Join-Path $BackupRoot 'original-gateway.sha256'),$OriginalHash,[Text.Encoding]::ASCII)
    $targetProcessRecord = [pscustomobject]@{ pid=$TargetSnapshot.Pid; owner_sid=$TargetSnapshot.OwnerSid; executable=$TargetSnapshot.Executable; command_line=$TargetSnapshot.CommandLine; start_utc=$TargetSnapshot.StartUtc.ToUniversalTime().ToString('o'); data_dir=$TargetSnapshot.DataDir } | ConvertTo-Json -Compress
    $targetProcessPath = Join-Path $BackupRoot 'target-process.json'
    [IO.File]::WriteAllText($targetProcessPath,$targetProcessRecord,[Text.UTF8Encoding]::new($false))
    Save-C201BackupMetadata $targetProcessPath $targetProcessPath
    $stateFile = Join-Path $DataDir 'managed-state.json'
    $PreStopStatePresent = Test-Path -LiteralPath $stateFile -PathType Leaf
    if ($PreStopStatePresent) {
        $PreStopStateHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $stateFile -ErrorAction Stop).Hash
        $PreStopStateSddl = (Get-Acl -LiteralPath $stateFile -ErrorAction Stop).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::All)
    } else { $PreStopStateHash = $null; $PreStopStateSddl = $null }
    $preStopSnapshot = [pscustomobject]@{ present=$PreStopStatePresent; hash=$PreStopStateHash; acl_sddl=$PreStopStateSddl } | ConvertTo-Json -Compress
    $preStopSnapshotPath = Join-Path $BackupRoot 'pre-stop-state.json'
    [IO.File]::WriteAllText($preStopSnapshotPath,$preStopSnapshot,[Text.UTF8Encoding]::new($false))
    Save-C201BackupMetadata $preStopSnapshotPath $preStopSnapshotPath
    $ManagedStateBackupReady = $false
    $NewLaunchAttempted = $false

Zastavení proběhne jen po novém ověření identity proti TargetSnapshot a přes handle otevřený pro konkrétní proces. Jakákoli nečitelná data nebo změna PID/cesty/argumentů/SID/času startu znamená nic nezastavovat. Před `Kill()` trvale zapiš `StopPending`; po úspěšném `WaitForExit` ihned nastav `$script:C201OriginalStopConfirmed=$true` a až pak ukládej `OriginalStopped`. Pokud zápis fáze po zastavení selže, trap má přímé potvrzení z handle a provede rollback. Při obnově v novém PowerShell procesu se `StopPending` porovná s uloženým PID/cestou/argumenty/SID/časem startu: přesně běžící původní proces znamená žádnou odstávku navíc, chybějící původní PID znamená pokračovat obnovou, změněná nebo nečitelná identita znamená `RecoveryFailed` bez zápisu.

    function Get-C201ProcessStartUtc($ProcessRow) {
        if ($ProcessRow.CreationDate -is [DateTime]) { return $ProcessRow.CreationDate.ToUniversalTime() }
        if (-not $ProcessRow.CreationDate) { throw 'Process creation time is unreadable.' }
        return [Management.ManagementDateTimeConverter]::ToDateTime([string]$ProcessRow.CreationDate).ToUniversalTime()
    }
    function Complete-C201ReturnedProcessBinding {
        $binding = $script:C201NewProcessBinding
        $h = $script:C201NewProcessHandle
        if (-not $binding -or -not $h -or [int]$h.Id -ne [int]$binding.pid) { throw 'Returned process handle is unavailable; cannot complete identity binding.' }
        $handleStart = $h.StartTime.ToUniversalTime()
        if ([long]$binding.start_utc_ticks -le 0 -or $handleStart.Ticks -ne [long]$binding.start_utc_ticks) { throw 'Returned process handle start time changed; identity binding refused.' }
        $rows = @(Get-CimInstance Win32_Process -Filter ('ProcessId=' + [int]$binding.pid) -ErrorAction Stop | Where-Object { $null -ne $_ })
        if ($h.HasExited) {
            if ($rows.Count -ne 0) { throw 'Returned process exited but its PID is occupied; possible PID reuse, no files will be restored.' }
            $binding.exited_confirmed = $true
            return $binding
        }
        if ($rows.Count -ne 1 -or -not $rows[0].ExecutablePath -or -not $rows[0].CommandLine) { throw 'Returned process CIM identity is missing or ambiguous.' }
        $row = $rows[0]
        $expectedExe = [IO.Path]::GetFullPath([string]$binding.executable)
        $expectedCommandLine = '"' + $expectedExe + '" ' + [string]$binding.arguments
        if ([int]$row.ProcessId -ne [int]$binding.pid -or [IO.Path]::GetFullPath([string]$row.ExecutablePath) -ne $expectedExe -or [string]$row.CommandLine -cne $expectedCommandLine) { throw 'Returned process CIM image or command line differs from the saved Start-Process binding.' }
        $creation = Get-C201ProcessStartUtc $row
        if ($creation -lt $script:C201NewLaunchAttemptUtc) { throw 'Returned process CIM creation time predates its launch attempt.' }
        $owner = $null
        $ownerFailed = $false
        try { $owner = Invoke-CimMethod -InputObject $row -MethodName GetOwner -ErrorAction Stop } catch { $ownerFailed = $true }
        if ($ownerFailed -or -not $owner -or $owner.ReturnValue -ne 0) {
            if (-not $h.HasExited) { throw 'Returned process owner lookup failed and the bound handle does not confirm exit; recovery stopped.' }
            if ($h.StartTime.ToUniversalTime().Ticks -ne [long]$binding.start_utc_ticks) { throw 'Returned process handle changed while owner lookup failed; recovery stopped.' }
            $pidRowsAfterOwnerFailure = @(Get-CimInstance Win32_Process -Filter ('ProcessId=' + [int]$binding.pid) -ErrorAction Stop | Where-Object { $null -ne $_ })
            if ($pidRowsAfterOwnerFailure.Count -ne 0) { throw 'Returned process exited during owner lookup but its PID is occupied; possible PID reuse, no files will be restored.' }
            if ([long]$binding.creation_utc_ticks -gt 0 -and [long]$binding.creation_utc_ticks -ne [long]$creation.Ticks) { throw 'Returned process creation time conflicts with the saved handle binding; recovery stopped.' }
            $binding.creation_utc_ticks = [long]$creation.Ticks
            $binding.exited_confirmed = $true
            $script:C201NewProcessBinding = $binding
            $null = Get-C201FreshInventoryAfterConfirmedExit $binding
            return $binding
        }
        $sid = ([Security.Principal.NTAccount]::new($owner.Domain,$owner.User)).Translate([Security.Principal.SecurityIdentifier]).Value
        if ($sid -ne [string]$binding.owner_sid) { throw 'Returned process owner differs from the saved Start-Process binding.' }
        $binding.creation_utc_ticks = [long]$creation.Ticks
        return $binding
    }
    function Assert-C201ReceiptMatchesBinding($Binding,$Identity) {
        if (-not $Identity) { return }
        $expectedExe = [IO.Path]::GetFullPath([string]$Binding.executable)
        $expectedCommandLine = '"' + $expectedExe + '" ' + [string]$Binding.arguments
        if ([string]$Identity.attempt_id -cne [string]$Binding.attempt_id -or [int]$Identity.pid -ne [int]$Binding.pid -or [string]$Identity.executable -cne $expectedExe -or [string]$Identity.command_line -cne $expectedCommandLine -or [string]$Identity.owner_sid -cne [string]$Binding.owner_sid -or [string]$Identity.data_dir -cne [IO.Path]::GetFullPath([string]$Binding.data_dir) -or [long]$Identity.start_utc_ticks -ne [long]$Binding.start_utc_ticks -or [long]$Binding.creation_utc_ticks -le 0 -or [long]$Identity.creation_utc_ticks -ne [long]$Binding.creation_utc_ticks) { throw 'Durable receipt and returned-process handle disagree; no process will be stopped.' }
    }
    function Get-C201FreshInventoryAfterConfirmedExit($Binding) {
        $h = $script:C201NewProcessHandle
        if (-not $Binding.exited_confirmed -or -not $h -or [int]$h.Id -ne [int]$Binding.pid -or -not $h.HasExited) { throw 'Returned process exit is not confirmed by its bound handle.' }
        $rows = @(Get-CimInstance Win32_Process -ErrorAction Stop)
        $pidRows = @($rows | Where-Object { [int]$_.ProcessId -eq [int]$Binding.pid })
        if ($pidRows.Count -gt 0) { throw 'Returned process PID became occupied after exit verification; possible PID reuse, no files will be restored.' }
        return $rows
    }
    function Resolve-C201ExitedCandidateAfterOwnerFailure([int]$CandidatePid,$Identity) {
        $h = $script:C201NewProcessHandle
        if (-not $Identity -or [int]$Identity.pid -ne $CandidatePid -or -not $h -or [int]$h.Id -ne $CandidatePid) { throw 'GetOwner failed for an unbound or ambiguous gateway candidate; no process or files will be changed.' }
        if (-not $h.HasExited) { throw 'GetOwner failed and the returned-process handle does not confirm exit; recovery stopped.' }
        $binding = Complete-C201ReturnedProcessBinding
        if (-not $binding.exited_confirmed) { throw 'GetOwner failed and process exit could not be confirmed; recovery stopped.' }
        Assert-C201ReceiptMatchesBinding $binding $Identity
        $script:C201NewProcessBinding = $binding
        return @(Get-C201FreshInventoryAfterConfirmedExit $binding)
    }
    function Stop-VerifiedProcess($Snapshot,[object]$BoundProcessHandle=$null,[switch]$RequireExactStartTime) {
        $ownsHandle = $null -eq $BoundProcessHandle
        $h = if ($ownsHandle) { Get-C201ProcessHandle ([int]$Snapshot.Pid) } else { $BoundProcessHandle }
        try {
            if ($h.HasExited) { throw 'Process already exited; no stop.' }
            $freshRows = @(Get-CimInstance Win32_Process -Filter ('ProcessId=' + $Snapshot.Pid) -ErrorAction Stop)
            if ($freshRows.Count -ne 1 -or -not $freshRows[0].ExecutablePath -or -not $freshRows[0].CommandLine) { throw 'Identity unreadable; no stop.' }
            $fresh = $freshRows[0]
            $freshOwner = Invoke-CimMethod -InputObject $fresh -MethodName GetOwner -ErrorAction Stop
            if ($freshOwner.ReturnValue -ne 0) { throw 'Owner unreadable; no stop.' }
            $freshSid = ([Security.Principal.NTAccount]::new($freshOwner.Domain,$freshOwner.User)).Translate([Security.Principal.SecurityIdentifier]).Value
            $handleExitedAtFinalCheck = [bool]$h.HasExited
            $handleStartUtc = $h.StartTime.ToUniversalTime()
            $expectedStartUtc = if ($Snapshot.StartUtc -is [DateTime]) { ([DateTime]$Snapshot.StartUtc).ToUniversalTime() } else { [DateTime]::Parse([string]$Snapshot.StartUtc).ToUniversalTime() }
            $freshStartUtc = if ($RequireExactStartTime) { Get-C201ProcessStartUtc $fresh } else { $handleStartUtc }
            $identityChanged = $freshSid -ne $Snapshot.OwnerSid -or [IO.Path]::GetFullPath($fresh.ExecutablePath) -ne [IO.Path]::GetFullPath([string]$Snapshot.Executable) -or $fresh.CommandLine -cne [string]$Snapshot.CommandLine
            if ($RequireExactStartTime) {
                $expectedCimStartTicks = [long]$Snapshot.CreationUtcTicks
                if ($handleExitedAtFinalCheck -or $identityChanged -or $handleStartUtc.Ticks -ne $expectedStartUtc.Ticks -or $freshStartUtc.Ticks -ne $expectedCimStartTicks) { throw 'PID reuse or exact process creation time changed at final stop check; no stop.' }
            } elseif ($handleExitedAtFinalCheck -or $identityChanged -or [Math]::Abs(($handleStartUtc-$expectedStartUtc).TotalSeconds) -gt 1) { throw 'Identity changed; no stop.' }
            $h.Kill()
            if (-not $h.WaitForExit(10000)) { throw 'Verified process did not stop.' }
        } finally {
            if ($ownsHandle) { try { $h.Dispose() } catch {} }
            # Keep the launch-bound handle available to rollback if the process
            # exits while Stop-VerifiedProcess is checking its final identity.
        }
    }
    function ConvertTo-C201ComparableDataDir([string]$Path) {
        if ([string]::IsNullOrWhiteSpace($Path)) { throw 'data-dir path is missing or not fully qualified.' }
        # Windows PowerShell/.NET Framework compatibility: IsPathRooted also accepts
        # drive-relative (C:foo) and current-drive-rooted (\foo) paths.
        $driveQualified = $Path -match '^[A-Za-z]:[\\/]'
        $uncQualified = $Path -match '^(?:\\\\|//)[^\\/]+[\\/][^\\/]+(?:[\\/]|$)'
        $extendedDriveQualified = $Path -match '^\\\\\?\\[A-Za-z]:\\'
        $extendedUncQualified = $Path -match '^\\\\\?\\UNC\\[^\\]+\\[^\\]+(?:\\|$)'
        if (-not ($driveQualified -or $uncQualified -or $extendedDriveQualified -or $extendedUncQualified)) { throw 'data-dir path is not fully qualified; its target cannot be verified.' }
        $fullPath = [IO.Path]::GetFullPath($Path)
        $rootPath = [IO.Path]::GetPathRoot($fullPath)
        if ([string]::IsNullOrWhiteSpace($rootPath)) { throw 'data-dir root could not be determined.' }
        if ($fullPath.Length -gt $rootPath.Length) {
            $fullPath = $fullPath.TrimEnd([char[]]@([IO.Path]::DirectorySeparatorChar,[IO.Path]::AltDirectorySeparatorChar))
            if ([string]::IsNullOrEmpty($fullPath)) { $fullPath = $rootPath }
        }
        return $fullPath
    }
    function Get-C201CommandLineTokens([string]$CommandLine) {
        if ([string]::IsNullOrWhiteSpace($CommandLine)) { throw 'Process command line is unreadable.' }
        if (-not ('C201RunbookCommandLine' -as [type])) {
            $nativeSource = @'
                using System;
                using System.Runtime.InteropServices;
                public static class C201RunbookCommandLine {
                    [DllImport("shell32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
                    public static extern IntPtr CommandLineToArgvW(string commandLine, out int argumentCount);
                    [DllImport("kernel32.dll", SetLastError = true)]
                    public static extern IntPtr LocalFree(IntPtr memory);
                }
    '@
            Add-Type -TypeDefinition $nativeSource -ErrorAction Stop
        }
        $argumentCount = 0
        $argumentVector = [C201RunbookCommandLine]::CommandLineToArgvW($CommandLine,[ref]$argumentCount)
        if ($argumentVector -eq [IntPtr]::Zero -or $argumentCount -lt 1) { throw 'Process command line could not be parsed.' }
        $arguments = [Collections.Generic.List[string]]::new()
        try {
            for ($index=0; $index -lt $argumentCount; $index++) {
                $argumentPointer = [Runtime.InteropServices.Marshal]::ReadIntPtr($argumentVector,$index * [IntPtr]::Size)
                if ($argumentPointer -eq [IntPtr]::Zero) { throw 'Process command line contains an unreadable argument.' }
                $arguments.Add([Runtime.InteropServices.Marshal]::PtrToStringUni($argumentPointer))
            }
        } finally { [void][C201RunbookCommandLine]::LocalFree($argumentVector) }
        return $arguments.ToArray()
    }
    function Get-C201DataDirArgument([string]$CommandLine) {
        $arguments = @(Get-C201CommandLineTokens $CommandLine)
        $values = [Collections.Generic.List[string]]::new()
        for ($index=1; $index -lt $arguments.Count; $index++) {
            $argument = [string]$arguments[$index]
            if ($argument -ceq '--') { break }
            $equalsMatch = [regex]::Match($argument,'^(?i)--?data-dir=(.*)$')
            if ($equalsMatch.Success) {
                $value = $equalsMatch.Groups[1].Value
                if ([string]::IsNullOrWhiteSpace($value)) { throw 'Gateway data-dir argument has no value.' }
                $values.Add($value)
            } elseif ($argument -match '^(?i)--?data-dir$') {
                if (($index + 1) -ge $arguments.Count) { throw 'Gateway data-dir argument is incomplete.' }
                $value = [string]$arguments[$index + 1]
                if ([string]::IsNullOrWhiteSpace($value) -or $value.StartsWith('-')) { throw 'Gateway data-dir argument is incomplete or ambiguous.' }
                $values.Add($value)
                $index++
            }
        }
        if ($values.Count -eq 0) { throw ('Gateway candidate data-dir unknown; parsed argument count=' + $arguments.Count + '.') }
        if ($values.Count -ne 1) { throw 'Gateway command line contains multiple data-dir arguments; use is ambiguous.' }
        return $values[0]
    }
    function Test-C201ManagedArgument([string]$CommandLine) {
        if ([string]::IsNullOrWhiteSpace($CommandLine)) { return $false }
        $arguments = @(Get-C201CommandLineTokens $CommandLine)
        for ($index=1; $index -lt $arguments.Count; $index++) {
            $argument = [string]$arguments[$index]
            if ($argument -ceq '--') { break }
            if ($argument -ceq '-managed' -or $argument -ceq '--managed') { return $true }
            $managedMatch = [regex]::Match($argument,'^--?managed=(.*)$')
            if (-not $managedMatch.Success) { continue }
            $value = $managedMatch.Groups[1].Value
            if ($value -cin @('1','t','T','TRUE','true','True')) { return $true }
            if ($value -cin @('0','f','F','FALSE','false','False')) { continue }
            # An unrecognized bool value is not accepted by Go flag.Parse();
            # treat the process as a candidate anyway and fail closed later.
            return $true
        }
        return $false
    }
    function Assert-DataDirUnused([string]$Directory) {
        $directoryPath = ConvertTo-C201ComparableDataDir $Directory
        $all = @(Get-CimInstance Win32_Process -ErrorAction Stop)
        foreach ($p in $all) {
            $candidate = ($p.Name -match '^(?i)xlg.*\.exe$|^gateway\.exe$') -or (Test-C201ManagedArgument ([string]$p.CommandLine))
            if (-not $candidate) { continue }
            if (-not $p.CommandLine) { throw 'Gateway process command line unreadable; data-dir use unknown.' }
            try { $candidateDir = Get-C201DataDirArgument ([string]$p.CommandLine) }
            catch { throw ('Gateway candidate data-dir unknown for PID ' + [string]$p.ProcessId + ' (' + [string]$p.Name + '): ' + $_.Exception.Message) }
            $candidatePath = ConvertTo-C201ComparableDataDir $candidateDir
            if ([string]::Equals($candidatePath,$directoryPath,[StringComparison]::OrdinalIgnoreCase)) { throw 'data-dir still in use.' }
        }
    }

Společnou funkci Start-C201Gateway definuj před odstávkou; spouští obě verze pod původním SID/DPAPI účtem, stejným working directory, argumenty, DataDir, tokenem, PATH a RTSP prostředím. Původní přítomnost či nepřítomnost `XLG_FFMPEG_PATH` se zachová přesně. Samostatná `$VerifiedFfmpegPath` se ověří před odstávkou a používá se pouze pro lokální RTSP kontrolu; nikdy se nezamění za prostředí gateway. Tokeny se nevypisují a rodičovské proměnné se obnoví ve finally.

    function Get-ViewerAccountInMemory {
        $path = Join-Path $env:APPDATA 'XiaomiViewer/config.json'
        $config = Get-Content -Raw -LiteralPath $path -ErrorAction Stop | ConvertFrom-Json
        $enabled = @($config.cameras | Where-Object { $_.model -eq 'xiaomi.camera.c201' -and $_.enabled })
        $stored = [string]$config.account.token
        if ($enabled.Count -ne 1 -or -not $config.account.user_id -or $config.account.region -ne 'de' -or -not $stored.StartsWith('dpapi:')) { throw 'DPAPI profile unavailable; STOP.' }
        $protected = [Convert]::FromBase64String($stored.Substring(6))
        Add-Type -AssemblyName System.Security
        $clear = [Security.Cryptography.ProtectedData]::Unprotect($protected,$null,[Security.Cryptography.DataProtectionScope]::CurrentUser)
        try {
            $token = [Text.Encoding]::UTF8.GetString($clear)
            if ([string]::IsNullOrWhiteSpace($token)) { throw 'DPAPI returned empty token; STOP.' }
            [pscustomobject]@{ user_id=[string]$config.account.user_id; region=[string]$config.account.region; token=$token }
        } finally { [Array]::Clear($clear,0,$clear.Length); [Array]::Clear($protected,0,$protected.Length) }
    }
    function Start-C201Gateway([string]$Executable,[string]$Stdout,[string]$Stderr) {
        $apiToken = [IO.File]::ReadAllText($TokenFile).Trim()
        if ($apiToken -notmatch '^[A-Fa-f0-9]{64}$') { throw 'API token invalid; STOP.' }
        $viewer = Get-ViewerAccountInMemory
        $names = @('XLG_API_TOKEN','XLG_XIAOMI_USER_ID','XLG_XIAOMI_REGION','XLG_XIAOMI_TOKEN','XLG_RTSP_ADVERTISE_HOST','XLG_RTSP_LISTEN','XLG_FFMPEG_PATH','PATH')
        $old = @{}
        foreach ($name in $names) { $old[$name] = [Environment]::GetEnvironmentVariable($name,'Process') }
        $p = $null
        try {
            [Environment]::SetEnvironmentVariable('XLG_API_TOKEN',$apiToken,'Process')
            [Environment]::SetEnvironmentVariable('XLG_XIAOMI_USER_ID',$viewer.user_id,'Process')
            [Environment]::SetEnvironmentVariable('XLG_XIAOMI_REGION',$viewer.region,'Process')
            [Environment]::SetEnvironmentVariable('XLG_XIAOMI_TOKEN',$viewer.token,'Process')
            [Environment]::SetEnvironmentVariable('XLG_RTSP_ADVERTISE_HOST',$RtspAdvertiseHost,'Process')
            [Environment]::SetEnvironmentVariable('XLG_RTSP_LISTEN',('0.0.0.0:' + $RtspPort),'Process')
            if ($GatewayFfmpegEnvPresent) { [Environment]::SetEnvironmentVariable('XLG_FFMPEG_PATH',$GatewayFfmpegEnvValue,'Process') }
            else { [Environment]::SetEnvironmentVariable('XLG_FFMPEG_PATH',$null,'Process') }
            [Environment]::SetEnvironmentVariable('PATH',$VerifiedPath,'Process')
            $args = '-managed -listen ' + $ApiListen + ' -data-dir "' + $DataDir + '"'
            $p = Start-Process -FilePath $Executable -ArgumentList $args -WorkingDirectory $VerifiedWorkingDirectory -WindowStyle Hidden -PassThru -RedirectStandardOutput $Stdout -RedirectStandardError $Stderr
            if ($p -and $script:C201ActiveLaunchRole -eq 'new') {
                $script:C201NewProcessHandle = $p
                $script:C201NewProcessBinding = [pscustomobject]@{ attempt_id=[string]$script:C201NewLaunchAttemptId; pid=[int]$p.Id; executable=[IO.Path]::GetFullPath($Executable); arguments=$args; owner_sid=[string]$OwnerSid; data_dir=[IO.Path]::GetFullPath($DataDir); start_utc_ticks=$null; creation_utc_ticks=$null; exited_confirmed=$false }
                $script:C201NewProcessBinding.start_utc_ticks = [long]$p.StartTime.ToUniversalTime().Ticks
            }
        } finally {
            foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name,$old[$name],'Process') }
            $apiToken = $null
            if ($viewer) { $viewer.user_id=$null; $viewer.region=$null; $viewer.token=$null }
        }
        if (-not $p -or $p.HasExited) { throw 'Gateway did not remain running; follow rollback, do not blindly retry.' }
        if ($script:C201ActiveLaunchRole -eq 'new') {
            if (-not $script:C201NewLaunchAttemptId -or -not $script:C201NewLaunchAttemptUtc) { throw 'New gateway launch has no durable attempt identity; leave process running and recover safely.' }
            $fullExecutable = [IO.Path]::GetFullPath($Executable)
            $expectedArguments = '-managed -listen ' + $ApiListen + ' -data-dir "' + $DataDir + '"'
            $expectedCommandLine = '"' + $fullExecutable + '" ' + $expectedArguments
            $started = $p.StartTime.ToUniversalTime()
            if (-not $script:C201NewProcessBinding -or [long]$script:C201NewProcessBinding.start_utc_ticks -ne $started.Ticks) { throw 'Returned process handle binding is incomplete; recovery required.' }
            if ($started -lt $script:C201NewLaunchAttemptUtc.ToUniversalTime()) { throw 'Returned new process predates this launch attempt; leave it running.' }
            $processRows = @(Get-CimInstance Win32_Process -Filter ('ProcessId=' + [int]$p.Id) -ErrorAction Stop)
            if ($processRows.Count -ne 1 -or -not $processRows[0].ExecutablePath -or -not $processRows[0].CommandLine) { throw 'Cannot bind returned process handle to one complete process identity; leave it running.' }
            $row = $processRows[0]
            $rowStart = Get-C201ProcessStartUtc $row
            $ownerResult = Invoke-CimMethod -InputObject $row -MethodName GetOwner -ErrorAction Stop
            if ($ownerResult.ReturnValue -ne 0) { throw 'Cannot verify owner for returned process handle; leave it running.' }
            $processSid = ([Security.Principal.NTAccount]::new($ownerResult.Domain,$ownerResult.User)).Translate([Security.Principal.SecurityIdentifier]).Value
            if ($p.HasExited -or [int]$row.ProcessId -ne [int]$p.Id -or $processSid -ne $OwnerSid -or [IO.Path]::GetFullPath($row.ExecutablePath) -ne $fullExecutable -or [string]$row.CommandLine -cne $expectedCommandLine) { throw 'Returned process handle does not match the new gateway identity; leave candidate running.' }
            $script:C201NewProcessBinding.creation_utc_ticks = [long]$rowStart.Ticks
            $receiptPath = Join-Path $BackupRoot 'new-process-receipt.json'
            if (Test-Path -LiteralPath $receiptPath) { throw 'New process receipt already exists; refusing to replace it.' }
            $receipt = [pscustomobject]@{ attempt_id=[string]$script:C201NewLaunchAttemptId; pid=[int]$p.Id; executable=$fullExecutable; command_line=$expectedCommandLine; owner_sid=$processSid; start_utc=$started.ToString('o'); start_utc_ticks=[long]$started.Ticks; creation_utc_ticks=[long]$rowStart.Ticks; data_dir=[IO.Path]::GetFullPath($DataDir) }
            $receiptTemp = $receiptPath + '.tmp'
            [IO.File]::WriteAllText($receiptTemp,($receipt | ConvertTo-Json -Compress),[Text.UTF8Encoding]::new($false))
            [IO.File]::Move($receiptTemp,$receiptPath)
            $storedReceipt = Get-Content -Raw -LiteralPath $receiptPath -ErrorAction Stop | ConvertFrom-Json
            if ($storedReceipt.attempt_id -cne $script:C201NewLaunchAttemptId) { throw 'New process receipt attempt ID verification failed; leave process running.' }
            if ([int]$storedReceipt.pid -ne [int]$p.Id) { throw 'New process receipt PID verification failed; leave process running.' }
            if ([string]$storedReceipt.command_line -cne $expectedCommandLine) { throw 'New process receipt command-line verification failed; leave process running.' }
            if ([string]$storedReceipt.owner_sid -ne $processSid) { throw 'New process receipt owner verification failed; leave process running.' }
            if ([long]$storedReceipt.start_utc_ticks -ne $started.Ticks) { throw 'New process receipt start-time verification failed; leave process running.' }
            if ([long]$storedReceipt.creation_utc_ticks -ne $rowStart.Ticks) { throw 'New process receipt CIM creation-time verification failed; leave process running.' }
            if ([IO.Path]::GetFullPath([string]$storedReceipt.data_dir) -ne [IO.Path]::GetFullPath($DataDir)) { throw 'New process receipt data-dir verification failed; leave process running.' }
            $script:C201NewProcessReceipt = $storedReceipt
        }
        return $p
    }

Všechny funkce volané při startu, cleanupu nebo rollbacku musí být definované před prvním zastavením procesu. Následující sdílené funkce vlož do stejného PowerShell běhu před `Stop-VerifiedProcess`. Při přerušení po odstávce pokračuj v rollback části podle uloženého stavu, nikoli od začátku nasazení.

    function Get-C201OriginalProcessState($Snapshot) {
        $rows = @(Get-CimInstance Win32_Process -ErrorAction Stop | Where-Object { $_.ProcessId -eq [int]$Snapshot.Pid })
        if ($rows.Count -eq 0) { return 'stopped' }
        if ($rows.Count -ne 1 -or -not $rows[0].ExecutablePath -or -not $rows[0].CommandLine) { throw 'Original process identity is ambiguous; recovery stopped.' }
        $row = $rows[0]
        $owner = Invoke-CimMethod -InputObject $row -MethodName GetOwner -ErrorAction Stop
        if ($owner.ReturnValue -ne 0) { throw 'Original process owner is unreadable; recovery stopped.' }
        $sid = ([Security.Principal.NTAccount]::new($owner.Domain,$owner.User)).Translate([Security.Principal.SecurityIdentifier]).Value
        $handle = Get-C201ProcessHandle ([int]$row.ProcessId)
        try {
            $same = -not $handle.HasExited -and $sid -eq [string]$Snapshot.OwnerSid -and [IO.Path]::GetFullPath($row.ExecutablePath) -eq [IO.Path]::GetFullPath([string]$Snapshot.Executable) -and $row.CommandLine -ceq [string]$Snapshot.CommandLine -and [Math]::Abs(($handle.StartTime.ToUniversalTime()-[DateTime]::Parse([string]$Snapshot.StartUtc).ToUniversalTime()).TotalSeconds) -le 1
            if (-not $same) { throw 'Original PID was reused or its identity changed; recovery stopped.' }
            return 'running'
        } finally { $handle.Dispose() }
    }
    function Assert-C201ExecutableUnused([string]$Executable) {
        $expected = [IO.Path]::GetFullPath($Executable)
        $all = @(Get-CimInstance Win32_Process -ErrorAction Stop)
        foreach ($p in $all) {
            $candidate = ($p.Name -match '^(?i)xlg.*\.exe$|^gateway\.exe$') -or ($p.CommandLine -and $p.CommandLine -match '(?i)(?:^|\s)-managed(?:\s|$)')
            if (-not $candidate) { continue }
            if (-not $p.ExecutablePath -or -not $p.CommandLine) { throw 'Gateway candidate identity unreadable; executable use is unknown.' }
            if ([IO.Path]::GetFullPath($p.ExecutablePath) -eq $expected) { throw 'Selected executable is in use by a running gateway; refuse overwrite.' }
        }
    }
    function Set-C201RunbookState([string]$NextState) {
        $current = [string]$script:C201RunbookState
        $allowed = @{
            Preflight=@('BackupReady','RecoveryFailed')
            BackupReady=@('StopPending','RecoveryFailed')
            StopPending=@('OriginalStopped','RecoveryFailed')
            OriginalStopped=@('ManagedStateReady','RolledBack','RecoveryFailed')
            ManagedStateReady=@('NewLaunchAttempted','RolledBack','RecoveryFailed')
            NewLaunchAttempted=@('NewStarted','RolledBack','RecoveryFailed')
            NewStarted=@('NewVerified','RolledBack','RecoveryFailed')
            NewVerified=@('RolledBack','RecoveryFailed')
            RolledBack=@()
            RecoveryFailed=@()
        }
        if ($current -eq $NextState) { return }
        if (-not $allowed.ContainsKey($current) -or $NextState -notin $allowed[$current]) { throw ('Illegal runbook state transition: ' + $current + ' -> ' + $NextState) }
        if ($script:C201RunbookStatePath) {
            $record = [pscustomobject]@{ state=$NextState; updated_utc=[DateTime]::UtcNow.ToString('o') } | ConvertTo-Json -Compress
            $temporary = $script:C201RunbookStatePath + '.tmp'
            [IO.File]::WriteAllText($temporary,$record,[Text.UTF8Encoding]::new($false))
            Move-Item -LiteralPath $temporary -Destination $script:C201RunbookStatePath -Force -ErrorAction Stop | Out-Null
        }
        # Persist first. If the atomic file replacement fails, in-memory state
        # stays at the last confirmed phase and trap reconciles the process handle.
        $script:C201RunbookState = $NextState
    }
    function Get-C201FailurePlan([string]$State,[bool]$NewLaunchAttempted,[bool]$ManagedStateBackupReady) {
        switch ($State) {
            { $_ -in @('Preflight','BackupReady') } { return [pscustomobject]@{ action='stop_without_restart'; stop_new=$false; restore_state=$false; verify_unchanged_state=$false; start_original=$false; exit_code=1 } }
            'StopPending' { return [pscustomobject]@{ action='recover_original'; stop_new=$false; restore_state=$false; verify_unchanged_state=$true; start_original=$true; exit_code=1 } }
            'OriginalStopped' { return [pscustomobject]@{ action='recover_original'; stop_new=$NewLaunchAttempted; restore_state=$ManagedStateBackupReady; verify_unchanged_state=(-not $ManagedStateBackupReady); start_original=$true; exit_code=1 } }
            'ManagedStateReady' { return [pscustomobject]@{ action='recover_original'; stop_new=$NewLaunchAttempted; restore_state=$true; verify_unchanged_state=$false; start_original=$true; exit_code=1 } }
            'NewLaunchAttempted' { return [pscustomobject]@{ action='recover_original'; stop_new=$true; restore_state=$true; verify_unchanged_state=$false; start_original=$true; exit_code=1 } }
            { $_ -in @('NewStarted','NewVerified') } {
                if (-not $ManagedStateBackupReady) { throw 'Persistent state backup is not verified; refuse recovery start.' }
                return [pscustomobject]@{ action='recover_original'; stop_new=$NewLaunchAttempted; restore_state=$true; verify_unchanged_state=$false; start_original=$true; exit_code=1 }
            }
            'RolledBack' { return [pscustomobject]@{ action='already_complete'; stop_new=$false; restore_state=$false; verify_unchanged_state=$false; start_original=$false; exit_code=1 } }
            default { throw ('Unknown runbook state; recovery is unsafe: ' + $State) }
        }
    }
    function Invoke-C201RuntimeCheck([int]$GatewayProcessId,[string]$Exe,[string]$Started,[string]$Stage) {
        $output = @(& $PowerShellExe -NoProfile -File $Verifier -ExpectedPid $GatewayProcessId -ExpectedExe $Exe -ExpectedStartUtc $Started -ExpectedDataDir $DataDir -ExpectedApiPort $ApiPort -CameraAddress $CameraAddress -Stage $Stage 2>&1)
        $code = $LASTEXITCODE
        $text = $output -join "`n"
        if ($code -ne 0) { throw ('Runtime verifier failed at ' + $Stage + '; STOP.') }
        if ($Stage -eq 'post-start') {
            $stateMatch = [regex]::Match($text,'RUNTIME_CHECK=PASS stage=post-start state=(empty|starting|ready)')
            if (-not $stateMatch.Success) { throw 'Post-start verifier returned no valid state; STOP.' }
            return $stateMatch.Groups[1].Value
        }
        if ($text -notmatch ('RUNTIME_CHECK=PASS stage=' + [regex]::Escape($Stage) + '\b')) { throw ('Runtime verifier result missing at ' + $Stage + '; STOP.') }
        return 'ready'
    }
    function Invoke-C201AuthenticatedGet([string]$Path) {
        $secret = [IO.File]::ReadAllText($TokenFile).Trim()
        if ($secret -notmatch '^[A-Fa-f0-9]{64}$') { throw 'API token file invalid; STOP.' }
        $headers = @{ Authorization = 'Bearer ' + $secret }
        try { return Invoke-RestMethod -Method Get -Uri ('http://' + $ApiListen + $Path) -Headers $headers -TimeoutSec 10 -MaximumRedirection 0 -ErrorAction Stop }
        catch { throw 'Authenticated API GET failed or timed out; result may be unknown and the request must not be repeated.' }
        finally { $secret=$null; $headers=$null }
    }
    function Assert-OneC201Stream($Inventory) {
        $c201Matches = @($Inventory.cameras | Where-Object { $_.model -eq 'xiaomi.camera.c201' })
        if ($c201Matches.Count -ne 1 -or $c201Matches[0].streams.Count -ne 1 -or $c201Matches[0].streams[0].protocol -ne 'rtsp' -or $c201Matches[0].streams[0].url -notmatch '^rtsp://') { throw 'Inventory did not contain exactly one C201 and one RTSP stream; STOP.' }
        return [pscustomobject]@{ CameraId=[string]$c201Matches[0].id; RTSPUrl=[string]$c201Matches[0].streams[0].url }
    }
    function Quote-C201Arg([string]$Value) { return '"' + $Value.Replace('"','\"') + '"' }
    function Restore-BackupIfChanged([string]$Name,[string]$Destination) {
        $verified = Assert-C201BackupArtifact $Name
        $source = $verified.Path
        $sddl = $verified.AclSddl
        $contentDifferent = -not (Test-Path -LiteralPath $Destination -PathType Leaf)
        if (-not $contentDifferent) { $contentDifferent = (Get-FileHash -Algorithm SHA256 -LiteralPath $source).Hash -ne (Get-FileHash -Algorithm SHA256 -LiteralPath $Destination).Hash }
        $aclDifferent = $true
        if (-not $contentDifferent) { $aclDifferent = (Get-Acl -LiteralPath $Destination -ErrorAction Stop).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::All) -ne $sddl }
        if ($contentDifferent) { Copy-Item -LiteralPath $source -Destination $Destination -Force -ErrorAction Stop }
        if ($contentDifferent -or $aclDifferent) {
            $acl = [Security.AccessControl.FileSecurity]::new()
            $acl.SetSecurityDescriptorSddlForm($sddl,[Security.AccessControl.AccessControlSections]::All)
            Set-Acl -LiteralPath $Destination -AclObject $acl
            if ((Get-FileHash -Algorithm SHA256 -LiteralPath $Destination).Hash -ne (Get-FileHash -Algorithm SHA256 -LiteralPath $source).Hash -or (Get-Acl -LiteralPath $Destination).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::All) -ne $sddl) { throw ('Restore hash/ACL mismatch: ' + $Name) }
        }
    }

    function Invoke-C201Rollback {
    # If resuming in a new PowerShell process after interruption, set BackupRoot
    # to the exact existing e1-backup-* directory, then rehydrate the durable stage.
    if (-not $script:C201RunbookStatePath) { $script:C201RunbookStatePath = Join-Path $BackupRoot 'runbook-state.json' }
    if (Test-Path -LiteralPath $script:C201RunbookStatePath -PathType Leaf) {
        $persistedRunbookState = Get-Content -Raw -LiteralPath $script:C201RunbookStatePath -ErrorAction Stop | ConvertFrom-Json
        if ($persistedRunbookState.state -notin @('Preflight','BackupReady','StopPending','OriginalStopped','ManagedStateReady','NewLaunchAttempted','NewStarted','NewVerified','RolledBack','RecoveryFailed')) { throw 'Unknown persisted runbook state; recovery stopped.' }
        $script:C201RunbookState = [string]$persistedRunbookState.state
        $ManagedStateBackupReady = $script:C201RunbookState -in @('ManagedStateReady','NewLaunchAttempted','NewStarted','NewVerified','RolledBack')
        $NewLaunchAttempted = $script:C201RunbookState -in @('NewLaunchAttempted','NewStarted','NewVerified')
    } else { throw 'Durable runbook state missing; recovery stopped.' }
    $targetRecordPath = Join-Path $BackupRoot 'target-process.json'
    if (-not (Test-Path -LiteralPath $targetRecordPath -PathType Leaf)) { throw 'Original process identity snapshot missing; recovery stopped.' }
    $targetRecord = Get-Content -Raw -LiteralPath $targetRecordPath -ErrorAction Stop | ConvertFrom-Json
    $TargetSnapshot = [pscustomobject]@{ Pid=[int]$targetRecord.pid; OwnerSid=[string]$targetRecord.owner_sid; Executable=[IO.Path]::GetFullPath([string]$targetRecord.executable); CommandLine=[string]$targetRecord.command_line; StartUtc=[DateTime]::Parse([string]$targetRecord.start_utc).ToUniversalTime(); DataDir=[IO.Path]::GetFullPath([string]$targetRecord.data_dir) }
    if ([IO.Path]::GetFullPath($TargetExe) -ne $TargetSnapshot.Executable -or [IO.Path]::GetFullPath($DataDir) -ne $TargetSnapshot.DataDir) { throw 'Rollback target differs from the verified pre-stop instance; recovery stopped.' }
    if ($script:C201RunbookState -ne 'RolledBack' -and ($script:C201RunbookState -in @('Preflight','BackupReady','StopPending') -or $script:C201OriginalStopConfirmed -or $script:C201RunbookState -in @('OriginalStopped','ManagedStateReady','NewLaunchAttempted','NewStarted','NewVerified'))) {
        $actualOriginalState = if ($script:C201OriginalStopConfirmed) { 'stopped' } else { Get-C201OriginalProcessState $TargetSnapshot }
        if ($actualOriginalState -eq 'running') {
            if ($script:C201RunbookState -in @('NewLaunchAttempted','NewStarted','NewVerified')) { throw 'Original gateway is running while replacement may exist; no files changed.' }
            $script:C201RecoveryOutcome = 'original_gateway_already_running; no_restart'
            return [pscustomobject]@{ state='OriginalRunning'; exit_code=1; action='stop_without_restart'; recovery=$script:C201RecoveryOutcome }
        }
        $script:C201OriginalStopConfirmed = $true
        if ($script:C201RunbookState -in @('Preflight','BackupReady','StopPending')) { $script:C201RunbookState = 'OriginalStopped' }
    }
    $preStopRecordPath = Join-Path $BackupRoot 'pre-stop-state.json'
    if (-not (Test-Path -LiteralPath $preStopRecordPath -PathType Leaf)) { throw 'Pre-stop state/ACL snapshot missing; recovery stopped.' }
    $preStopRecord = Get-Content -Raw -LiteralPath $preStopRecordPath -ErrorAction Stop | ConvertFrom-Json
    $PreStopStatePresent = [bool]$preStopRecord.present
    $PreStopStateHash = [string]$preStopRecord.hash
    $PreStopStateSddl = [string]$preStopRecord.acl_sddl
    $launchAttemptPath = Join-Path $BackupRoot 'new-launch-attempt.utc'
    $launchAttemptRecordPath = Join-Path $BackupRoot 'new-launch-attempt.json'
    $launchAttemptRecord = $null
    if ($NewLaunchAttempted) {
        if (-not (Test-Path -LiteralPath $launchAttemptPath -PathType Leaf)) { throw 'New launch timestamp missing; cannot identify a process safely.' }
        $NewLaunchAttemptUtc = [DateTime]::Parse([IO.File]::ReadAllText($launchAttemptPath)).ToUniversalTime()
        $script:C201NewLaunchAttemptUtc = $NewLaunchAttemptUtc
        if (-not (Test-Path -LiteralPath $launchAttemptRecordPath -PathType Leaf)) { throw 'New launch attempt identity missing; recovery cannot attribute any matching process safely.' }
        $launchAttemptRecord = Get-Content -Raw -LiteralPath $launchAttemptRecordPath -ErrorAction Stop | ConvertFrom-Json
        $expectedAttemptArguments = '-managed -listen ' + $ApiListen + ' -data-dir "' + $DataDir + '"'
        if (-not $launchAttemptRecord.attempt_id -or [long]$launchAttemptRecord.started_utc_ticks -ne $NewLaunchAttemptUtc.Ticks -or [IO.Path]::GetFullPath([string]$launchAttemptRecord.executable) -ne [IO.Path]::GetFullPath($NewExe) -or [string]$launchAttemptRecord.arguments -cne $expectedAttemptArguments -or [string]$launchAttemptRecord.owner_sid -ne $OwnerSid -or [IO.Path]::GetFullPath([string]$launchAttemptRecord.data_dir) -ne [IO.Path]::GetFullPath($DataDir)) { throw 'New launch attempt record does not match this run; recovery stopped.' }
        $script:C201NewLaunchAttemptId = [string]$launchAttemptRecord.attempt_id
    }
    $failurePlan = Get-C201FailurePlan $script:C201RunbookState $NewLaunchAttempted $ManagedStateBackupReady
    if ($failurePlan.action -eq 'stop_without_restart') { return [pscustomobject]@{ state=$script:C201RunbookState; exit_code=1; action='stop_without_restart' } }
    if ($failurePlan.action -eq 'already_complete') {
        $markerPath = Join-Path $BackupRoot 'rollback-complete.json'
        if (-not (Test-Path -LiteralPath $markerPath -PathType Leaf)) { throw 'Rollback state says complete but marker is absent; RecoveryFailed.' }
        $done = Get-Content -Raw -LiteralPath $markerPath -ErrorAction Stop | ConvertFrom-Json
        if ($done.state -ne 'RolledBack' -or [IO.Path]::GetFullPath([string]$done.executable) -ne [IO.Path]::GetFullPath($TargetExe) -or [IO.Path]::GetFullPath([string]$done.data_dir) -ne [IO.Path]::GetFullPath($DataDir)) { throw 'Rollback completion marker does not match this target; RecoveryFailed.' }
        $null = Invoke-C201RuntimeCheck ([int]$done.pid) $TargetExe ([string]$done.start_utc) 'pre-inventory'
        [Console]::WriteLine('C201_ROLLBACK=ALREADY_COMPLETE state=RolledBack exit_code=1')
        return [pscustomobject]@{ state='RolledBack'; exit_code=1; action='already_complete' }
    }
    if ($failurePlan.action -ne 'recover_original') { throw 'Rollback plan is unsafe; RecoveryFailed.' }
    # Validate the whole recovery set before candidate termination or any
    # write/delete to the original executable, persistent state, or private files.
    $null = Assert-C201RollbackBackupSet $failurePlan $TargetSnapshot $PreStopStatePresent $PreStopStateHash $PreStopStateSddl
    $expectedNewArgs = '-managed -listen ' + $ApiListen + ' -data-dir "' + $DataDir + '"'
    $processRows = @(Get-CimInstance Win32_Process -ErrorAction Stop)
    $matchingNewCandidates = @()
    $exactNew = @()
    $newReceiptPath = Join-Path $BackupRoot 'new-process-receipt.json'
    $newReceipt = $null
    $newIdentity = $null
    $expectedReceiptCommandLine = '"' + [IO.Path]::GetFullPath($NewExe) + '" ' + $expectedNewArgs
    if ($NewLaunchAttempted -and (Test-Path -LiteralPath $newReceiptPath -PathType Leaf)) {
        $newReceipt = Get-Content -Raw -LiteralPath $newReceiptPath -ErrorAction Stop | ConvertFrom-Json
        if ([string]$newReceipt.attempt_id -cne $script:C201NewLaunchAttemptId -or [IO.Path]::GetFullPath([string]$newReceipt.executable) -ne [IO.Path]::GetFullPath($NewExe) -or [string]$newReceipt.command_line -cne $expectedReceiptCommandLine -or [string]$newReceipt.owner_sid -ne $OwnerSid -or [IO.Path]::GetFullPath([string]$newReceipt.data_dir) -ne [IO.Path]::GetFullPath($DataDir) -or [long]$newReceipt.start_utc_ticks -le 0 -or [long]$newReceipt.creation_utc_ticks -le 0) { throw 'New process receipt is inconsistent; no candidate will be stopped.' }
        $receiptStart = [DateTime]::new([long]$newReceipt.start_utc_ticks,[DateTimeKind]::Utc)
        if ($receiptStart -lt $NewLaunchAttemptUtc) { throw 'New process receipt predates its launch attempt; no candidate will be stopped.' }
        $newIdentity = [pscustomobject]@{ attempt_id=[string]$newReceipt.attempt_id; pid=[int]$newReceipt.pid; executable=[IO.Path]::GetFullPath([string]$newReceipt.executable); command_line=[string]$newReceipt.command_line; owner_sid=[string]$newReceipt.owner_sid; data_dir=[IO.Path]::GetFullPath([string]$newReceipt.data_dir); start_utc_ticks=[long]$newReceipt.start_utc_ticks; creation_utc_ticks=[long]$newReceipt.creation_utc_ticks }
    }
    if ($script:C201NewProcessBinding) {
        $binding = $script:C201NewProcessBinding
        if ([string]$binding.attempt_id -cne $script:C201NewLaunchAttemptId -or [IO.Path]::GetFullPath([string]$binding.executable) -ne [IO.Path]::GetFullPath($NewExe) -or [string]$binding.arguments -cne $expectedNewArgs -or [string]$binding.owner_sid -ne $OwnerSid -or [IO.Path]::GetFullPath([string]$binding.data_dir) -ne [IO.Path]::GetFullPath($DataDir) -or [long]$binding.start_utc_ticks -le 0) { throw 'In-memory returned-process binding is inconsistent; no candidate will be stopped.' }
        if ([long]$binding.creation_utc_ticks -le 0 -or $script:C201NewProcessHandle.HasExited) { $binding = Complete-C201ReturnedProcessBinding }
        if ($binding.exited_confirmed) {
            Assert-C201ReceiptMatchesBinding $binding $newIdentity
            $script:C201NewProcessBinding = $binding
            # The receipt may be valid even though the process later exited.
            # Reconcile it with the same launch-bound handle, then discard the
            # stale inventory before checking candidates and data-dir use.
            $newIdentity = $null
            $processRows = @(Get-C201FreshInventoryAfterConfirmedExit $binding)
        } else {
            if ([long]$binding.creation_utc_ticks -le 0) { throw 'Returned process creation-time binding is incomplete; no candidate will be stopped.' }
            $boundIdentity = [pscustomobject]@{ attempt_id=[string]$binding.attempt_id; pid=[int]$binding.pid; executable=[IO.Path]::GetFullPath([string]$binding.executable); command_line=('"' + [IO.Path]::GetFullPath([string]$binding.executable) + '" ' + [string]$binding.arguments); owner_sid=[string]$binding.owner_sid; data_dir=[IO.Path]::GetFullPath([string]$binding.data_dir); start_utc_ticks=[long]$binding.start_utc_ticks; creation_utc_ticks=[long]$binding.creation_utc_ticks }
            Assert-C201ReceiptMatchesBinding $binding $newIdentity
            if (-not $newIdentity) { $newIdentity = $boundIdentity }
        }
    }
    $refreshCandidatesAfterExit = $false
    do {
    $refreshCandidatesAfterExit = $false
    $matchingNewCandidates = @()
    $exactNew = @()
    if ($NewLaunchAttempted) { foreach ($row in $processRows) {
        $knownGateway = ($row.Name -match '^(?i)xlg.*\.exe$|^gateway\.exe$') -or ($row.CommandLine -and $row.CommandLine -match '(?i)(?:^|\s)-managed(?:\s|$)')
        if (-not $knownGateway) { continue }
        if (-not $row.CommandLine -or -not $row.ExecutablePath) { throw 'Gateway candidate unreadable; no rollback stop.' }
        $candidateExe = [IO.Path]::GetFullPath($row.ExecutablePath)
        if (-not [string]::Equals($candidateExe,$NewExe,[StringComparison]::OrdinalIgnoreCase)) { continue }
        $prefix = '"' + $candidateExe + '" '
        if ($row.CommandLine.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)) { $candidateArgs = $row.CommandLine.Substring($prefix.Length) }
        elseif ($row.CommandLine.StartsWith(($candidateExe + ' '),[StringComparison]::OrdinalIgnoreCase)) { $candidateArgs = $row.CommandLine.Substring($candidateExe.Length + 1) }
        else { throw 'New executable command line ambiguous; no rollback stop.' }
        if ($candidateArgs -cne $expectedNewArgs) { continue }
        try { $ownerResult = Invoke-CimMethod -InputObject $row -MethodName GetOwner -ErrorAction Stop }
        catch {
            $processRows = @(Resolve-C201ExitedCandidateAfterOwnerFailure ([int]$row.ProcessId) $newIdentity)
            $newIdentity = $null
            $refreshCandidatesAfterExit = $true
            break
        }
        if ($ownerResult.ReturnValue -ne 0) {
            $processRows = @(Resolve-C201ExitedCandidateAfterOwnerFailure ([int]$row.ProcessId) $newIdentity)
            $newIdentity = $null
            $refreshCandidatesAfterExit = $true
            break
        }
        $candidateSid = ([Security.Principal.NTAccount]::new($ownerResult.Domain,$ownerResult.User)).Translate([Security.Principal.SecurityIdentifier]).Value
        if ($candidateSid -ne $OwnerSid) { throw 'A same-path/same-arguments gateway candidate has a different owner; no process will be stopped.' }
        $candidateRecord = [pscustomobject]@{ Pid=[int]$row.ProcessId; OwnerSid=$candidateSid; Executable=$candidateExe; CommandLine=[string]$row.CommandLine; StartUtc=$null }
        $matchingNewCandidates += $candidateRecord
        if ($newIdentity -and [int]$newIdentity.pid -eq [int]$row.ProcessId) {
            $candidateProc = $script:C201NewProcessHandle
            if (-not $candidateProc -or [int]$candidateProc.Id -ne [int]$row.ProcessId) { $candidateProc = Get-C201ProcessHandle ([int]$row.ProcessId); $script:C201NewProcessHandle = $candidateProc }
            if ($candidateProc.HasExited) {
                $binding = Complete-C201ReturnedProcessBinding
                if (-not $binding.exited_confirmed) { throw 'New process exit was not confirmed; no rollback restoration.' }
                Assert-C201ReceiptMatchesBinding $binding $newIdentity
                $script:C201NewProcessBinding = $binding
                $newIdentity = $null
                $processRows = @(Get-C201FreshInventoryAfterConfirmedExit $binding)
                $refreshCandidatesAfterExit = $true
                break
            }
            $candidateStart = $candidateProc.StartTime.ToUniversalTime()
            $rowStart = Get-C201ProcessStartUtc $row
            $receiptStart = [DateTime]::new([long]$newIdentity.start_utc_ticks,[DateTimeKind]::Utc)
            if ($candidateStart.Ticks -ne [long]$newIdentity.start_utc_ticks -or $rowStart.Ticks -ne [long]$newIdentity.creation_utc_ticks -or $candidateStart -lt $NewLaunchAttemptUtc -or [string]$row.CommandLine -cne [string]$newIdentity.command_line) { throw 'New process handle, current creation time or durable identity differs; no process will be stopped.' }
            $candidateRecord.StartUtc = $candidateStart
            $exactNew += $candidateRecord
        }
    } }
    } while ($refreshCandidatesAfterExit)
    if ($NewLaunchAttempted -and -not $newIdentity -and $matchingNewCandidates.Count -gt 0) { throw 'A gateway matches the attempted executable, arguments and owner but no returned-handle receipt or in-memory process handle proves this run started it; leave candidate running and report RecoveryFailed.' }
    if ($newIdentity -and $matchingNewCandidates.Count -gt 1) { throw 'Multiple same-owner gateways match the selected executable and arguments; no process will be stopped.' }
    if ($newIdentity -and $matchingNewCandidates.Count -eq 1 -and $exactNew.Count -ne 1) { throw 'A same-owner gateway candidate exists but does not match the saved returned-handle receipt; leave it running.' }
    if ($newIdentity) {
        $receiptPidRows = @($processRows | Where-Object { $_.ProcessId -eq [int]$newIdentity.pid })
        if ($receiptPidRows.Count -gt 1) { throw 'Receipt PID is ambiguous; no process will be stopped.' }
        if ($receiptPidRows.Count -eq 1 -and ($exactNew.Count -ne 1 -or $exactNew[0].Pid -ne [int]$newIdentity.pid)) { throw 'Receipt PID was reused or identity differs; do not stop it.' }
    }
    if ($exactNew.Count -gt 1) { throw 'Multiple exact new processes; no rollback stop.' }
    if ($NewPid -gt 0) {
        $pidRows = @($processRows | Where-Object { $_.ProcessId -eq $NewPid })
        if ($pidRows.Count -gt 1) { throw 'Recorded new PID is ambiguous.' }
        if ($pidRows.Count -eq 1 -and ($exactNew.Count -ne 1 -or $exactNew[0].Pid -ne $NewPid)) { throw 'Recorded PID was reused or identity differs; do not stop it.' }
    }
    if ($failurePlan.stop_new -and $exactNew.Count -eq 1) {
        $stopSnapshot = [pscustomobject]@{ Pid=[int]$newIdentity.pid; OwnerSid=[string]$newIdentity.owner_sid; Executable=[IO.Path]::GetFullPath([string]$newIdentity.executable); CommandLine=[string]$newIdentity.command_line; StartUtc=[DateTime]::new([long]$newIdentity.start_utc_ticks,[DateTimeKind]::Utc); CreationUtcTicks=[long]$newIdentity.creation_utc_ticks }
        try {
            Stop-VerifiedProcess $stopSnapshot $script:C201NewProcessHandle -RequireExactStartTime
            try { $script:C201NewProcessHandle.Dispose() } catch {}
            $script:C201NewProcessHandle = $null
        }
        catch {
            $boundHandle = $script:C201NewProcessHandle
            if (-not $boundHandle -or -not $boundHandle.HasExited) { throw }
            $binding = Complete-C201ReturnedProcessBinding
            if (-not $binding.exited_confirmed) { throw }
            Assert-C201ReceiptMatchesBinding $binding $newIdentity
            $script:C201NewProcessBinding = $binding
            $newIdentity = $null
            $processRows = @(Get-C201FreshInventoryAfterConfirmedExit $binding)
        }
    }
    $script:C201RecoveryOutcome = 'original_stopped; checking_exclusive_data_and_binary_access'
    Assert-DataDirUnused $DataDir
    Assert-C201ExecutableUnused $TargetExe
    $backupState = Join-Path $BackupRoot 'managed-state.json'
    $stateFile = Join-Path $DataDir 'managed-state.json'
    if ($failurePlan.verify_unchanged_state) {
        $currentStatePresent = Test-Path -LiteralPath $stateFile -PathType Leaf
        if ($currentStatePresent -ne $PreStopStatePresent) { throw 'State presence differs from pre-stop snapshot; refuse old start.' }
        if ($PreStopStatePresent) {
            $currentStateHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $stateFile -ErrorAction Stop).Hash
            $currentStateSddl = (Get-Acl -LiteralPath $stateFile -ErrorAction Stop).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::All)
            if ($currentStateHash -ne $PreStopStateHash -or $currentStateSddl -ne $PreStopStateSddl) { throw 'State/ACL differs from pre-stop snapshot; refuse old start.' }
        }
    } elseif ($failurePlan.restore_state -and (Test-Path -LiteralPath $backupState -PathType Leaf)) {
        Copy-Item -LiteralPath $backupState -Destination $stateFile -Force -ErrorAction Stop
        $sddl = [IO.File]::ReadAllText($backupState + '.acl.sddl')
        $acl = [Security.AccessControl.FileSecurity]::new()
        $acl.SetSecurityDescriptorSddlForm($sddl,[Security.AccessControl.AccessControlSections]::All)
        Set-Acl -LiteralPath $stateFile -AclObject $acl
        if ((Get-FileHash -Algorithm SHA256 -LiteralPath $stateFile).Hash -ne (Get-FileHash -Algorithm SHA256 -LiteralPath $backupState).Hash -or (Get-Acl -LiteralPath $stateFile).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::All) -ne $sddl) { throw 'State/ACL restore mismatch; do not start.' }
    } elseif ($failurePlan.restore_state -and (Test-Path -LiteralPath (Join-Path $BackupRoot 'managed-state.absent'))) {
        if (Test-Path -LiteralPath $stateFile) { Remove-Item -LiteralPath $stateFile -Force -ErrorAction Stop }
    } elseif ($failurePlan.restore_state) { throw 'No valid managed-state backup; do not start.' }
    if ($failurePlan.restore_state) { foreach ($name in @('api-token.txt','firewall-target.json','XiaomiViewer-config.dpapi.json','gateway.stderr.log','gateway.stdout.log','gateway.pid')) {
        $destination = switch ($name) {
            'api-token.txt' { $TokenFile }
            'firewall-target.json' { $LaunchFile }
            'XiaomiViewer-config.dpapi.json' { $ConfigFile }
            default { Join-Path $PrivateDir $name }
        }
        $live = $destination
        if ($name -in @('gateway.stderr.log','gateway.stdout.log') -and (Test-Path -LiteralPath $live -PathType Leaf)) { Copy-Item -LiteralPath $live -Destination (Join-Path $BackupRoot ('failed-new-' + $name)) -Force -ErrorAction Stop }
        $saved = Join-Path $BackupRoot $name
        if (Test-Path -LiteralPath $saved -PathType Leaf) { Restore-BackupIfChanged $name $destination }
        elseif ($name -in @('api-token.txt','firewall-target.json','XiaomiViewer-config.dpapi.json')) { throw ('Required private backup missing: ' + $name) }
    } }
    $oldBinaryBackup = Join-Path $BackupRoot 'original-gateway.exe'
    $oldBinaryHashFile = Join-Path $BackupRoot 'original-gateway.sha256'
    $oldBinaryAclFile = $oldBinaryBackup + '.acl.sddl'
    if ([IO.Path]::GetFullPath($TargetExe) -ne $TargetSnapshot.Executable -or -not (Test-Path -LiteralPath $oldBinaryBackup -PathType Leaf) -or -not (Test-Path -LiteralPath $oldBinaryHashFile -PathType Leaf) -or -not (Test-Path -LiteralPath $oldBinaryAclFile -PathType Leaf)) { throw 'Verified original executable/ACL backup is incomplete; do not start.' }
    $OriginalHash = [IO.File]::ReadAllText($oldBinaryHashFile).Trim()
    if ($OriginalHash -notmatch '^[A-Fa-f0-9]{64}$' -or (Get-FileHash -Algorithm SHA256 -LiteralPath $oldBinaryBackup).Hash -ne $OriginalHash) { throw 'Original executable backup hash mismatch; do not start.' }
    if ((Get-C201OriginalProcessState $TargetSnapshot) -ne 'stopped') { throw 'Original executable is still in use by the selected process; do not restore.' }
    Assert-C201ExecutableUnused $TargetExe
    Restore-BackupIfChanged 'original-gateway.exe' $TargetExe
    $originalAclSddl = [IO.File]::ReadAllText($oldBinaryAclFile)
    if ((Get-FileHash -Algorithm SHA256 -LiteralPath $TargetExe).Hash -ne $OriginalHash -or (Get-Acl -LiteralPath $TargetExe -ErrorAction Stop).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::All) -ne $originalAclSddl) { throw 'Original executable hash/ACL restore did not verify; do not start.' }
    $script:C201RecoveryOutcome = 'original_binary_and_persistent_state_restored; original_gateway=stopped'
    $oldStdout = Join-Path $PrivateDir 'gateway.stdout.log'
    $oldStderr = Join-Path $PrivateDir 'gateway.stderr.log'
    $oldProcess = Start-C201Gateway $TargetExe $oldStdout $oldStderr
    $script:C201RestartedOriginalProcess = $oldProcess
    $oldArguments = '-managed -listen ' + $ApiListen + ' -data-dir "' + $DataDir + '"'
    $script:C201RestartedOriginalSnapshot = [pscustomobject]@{ Pid=[int]$oldProcess.Id; OwnerSid=$OwnerSid; Executable=[IO.Path]::GetFullPath($TargetExe); CommandLine=('"' + [IO.Path]::GetFullPath($TargetExe) + '" ' + $oldArguments); StartUtc=$oldProcess.StartTime.ToUniversalTime(); DataDir=$DataDir }
    $script:C201RecoveryOutcome = 'original_gateway=running_unverified; readiness=pending'
    $oldManifest = Get-Content -Raw -LiteralPath $LaunchFile | ConvertFrom-Json
    $oldManifest.gateway_pid = $oldProcess.Id
    $oldManifest.gateway_executable = $TargetExe
    [IO.File]::WriteAllText($LaunchFile,($oldManifest | ConvertTo-Json -Depth 10),[Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $PrivateDir 'gateway.pid'),[string]$oldProcess.Id,[Text.Encoding]::ASCII)
    $OldPid = $oldProcess.Id
    $OldStartUtcText = $oldProcess.StartTime.ToUniversalTime().ToString('o')
    # Rollback uses the same no-Bearer post-start gate and at-most-once
    # initializer flow as the new binary. No PTZ request is part of rollback.
    $rollbackState = Invoke-C201RuntimeCheck $OldPid $TargetExe $OldStartUtcText 'post-start'
    $rollbackInventory = $null
    if ($rollbackState -eq 'empty') { $rollbackInventory = Invoke-C201AuthenticatedGet '/v2/cameras' }
    if ($rollbackState -notin @('empty','starting','ready')) { throw 'Rollback startup state is ambiguous; leave state and report.' }
    $rollbackDeadline = [DateTime]::UtcNow.AddSeconds(30)
    while ($rollbackState -ne 'ready' -and [DateTime]::UtcNow -lt $rollbackDeadline) {
        Start-Sleep -Seconds 1
        $rollbackState = Invoke-C201RuntimeCheck $OldPid $TargetExe $OldStartUtcText 'post-start'
        if ($rollbackState -notin @('empty','starting','ready')) { throw 'Rollback session state is ambiguous; leave state and report.' }
    }
    if ($rollbackState -ne 'ready') { throw 'Rollback timed out before one ready CS2/TCP session; no initializer retry.' }
    if (-not $rollbackInventory) {
        $null = Invoke-C201RuntimeCheck $OldPid $TargetExe $OldStartUtcText 'pre-inventory'
        $rollbackInventory = Invoke-C201AuthenticatedGet '/v2/cameras'
    }
    $rollbackStream = Assert-OneC201Stream $rollbackInventory
    $null = Invoke-C201RuntimeCheck $OldPid $TargetExe $OldStartUtcText 'pre-inventory'
    $rollbackInfo = Invoke-C201AuthenticatedGet '/v2/info'
    if (-not $rollbackInfo.instance_id -or $rollbackInfo.api_version -ne '2.0') { throw 'Rollback managed API identity/version check failed; leave state and report.' }
    $rollbackInfo = $null
    if ($rollbackStream.RTSPUrl -match '[\r\n"]') { throw 'Rollback RTSP URL contains an unsafe argument character; STOP.' }
    $rollbackStamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ')
    $rollbackProgress = Join-Path $PrivateDir ('rollback-rtsp-progress-' + $rollbackStamp + '.txt')
    $rollbackErrors = Join-Path $PrivateDir ('rollback-rtsp-' + $rollbackStamp + '.stderr.log')
    $rollbackArgs = '-nostdin -hide_banner -loglevel error -progress pipe:1 -nostats -rtsp_transport tcp -i ' + (Quote-C201Arg $rollbackStream.RTSPUrl) + ' -map 0:v:0 -an -frames:v 8 -f null NUL'
    $rollbackFrames = Start-Process -FilePath $VerifiedFfmpegPath -ArgumentList $rollbackArgs -WorkingDirectory $VerifiedWorkingDirectory -WindowStyle Hidden -PassThru -Wait -RedirectStandardOutput $rollbackProgress -RedirectStandardError $rollbackErrors
    $rollbackFrameRows = @(Get-Content -LiteralPath $rollbackProgress -ErrorAction Stop | Where-Object { $_ -match '^frame=\d+$' })
    if ($rollbackFrames.ExitCode -ne 0 -or $rollbackFrameRows.Count -eq 0 -or $rollbackFrameRows[-1] -ne 'frame=8') { throw 'Rollback RTSP did not decode eight frames; stop and report.' }
    $null = Invoke-C201RuntimeCheck $OldPid $TargetExe $OldStartUtcText 'pre-inventory'
    $script:C201RecoveryOutcome = 'original_gateway_verified'
    Set-C201RunbookState 'RolledBack'
    $rollbackMarker = [pscustomobject]@{ state='RolledBack'; pid=$OldPid; executable=$TargetExe; start_utc=$OldStartUtcText; data_dir=$DataDir; updated_utc=[DateTime]::UtcNow.ToString('o') } | ConvertTo-Json -Compress
    [IO.File]::WriteAllText((Join-Path $BackupRoot 'rollback-complete.json'),$rollbackMarker,[Text.UTF8Encoding]::new($false))
    $script:C201RollbackCompleted = $true
    $script:C201RecoveryOutcome = 'verified_rollback_complete'
    [Console]::WriteLine('C201_ROLLBACK=PASS state=RolledBack exit_code=1')
    return [pscustomobject]@{ state='RolledBack'; exit_code=1; action='recovered' }
    }
    Set-C201RunbookState 'Preflight'
    Set-C201RunbookState 'BackupReady'
    Set-C201RunbookState 'StopPending'

Po zastavení původního procesu a po úspěšném Assert-DataDirUnused zálohuj konzistentní stav/ACL a logy. Pokud soubor stavu chybí, zaznamenej marker absent. Pokud záloha selže, novou verzi nespouštěj; spusť původní executable se stavem ponechaným na místě.

    Stop-VerifiedProcess $TargetSnapshot
    $script:C201OriginalStopConfirmed = $true
    Set-C201RunbookState 'OriginalStopped'
    Assert-DataDirUnused $DataDir
    $stateFile = Join-Path $DataDir 'managed-state.json'
    try {
        if (Test-Path -LiteralPath $stateFile -PathType Leaf) { Save-BackupFile $stateFile 'managed-state.json' }
        else { [IO.File]::WriteAllText((Join-Path $BackupRoot 'managed-state.absent'),'absent',[Text.UTF8Encoding]::new($false)) }
        foreach ($name in @('gateway.stderr.log','gateway.stdout.log','gateway.pid')) {
            $p = Join-Path $PrivateDir $name
            if (Test-Path -LiteralPath $p -PathType Leaf) { Save-BackupFile $p $name }
        }
        $ManagedStateBackupReady = $true
        Set-C201RunbookState 'ManagedStateReady'
    } catch {
        throw 'Post-stop state/log backup failed; invoke the state-aware rollback dispatcher. New version was not started.'
    }

Před startem ověř znovu SHA-256 a backup. Nová executable má samostatnou cestu; původní binary se nepřepisuje. Po startu aktualizuj PID/executable v manifestu při zachování ostatních polí a ACL. Volej `post-start` ještě před jakýmkoli Bearer požadavkem. Při `empty` odešli právě jeden inicializační `GET /v2/cameras`; při timeoutu/výjimce jeho výsledek považuj za neznámý a GET neopakuj. Při `starting` pouze čekej, při `ready` inicializační GET přeskoč. Potom v omezeném intervalu pouze opakuj read-only `post-start` kontrolu do `ready`; `empty` po initializeru může během jeho asynchronního startu krátce přetrvat, jiné/nejasné stavy znamenají STOP. Po `ready` spusť `pre-inventory`, který potvrzuje jednu session a čerstvou cache. Pokud inventory není uložené z initializeru, smí být jeden výpisový GET `/v2/cameras` až po této kontrole; ihned po něm znovu ověř `pre-inventory`. Z odpovědi drž pouze v paměti právě jednu kameru `model=xiaomi.camera.c201` a její jediný RTSP stream; URL nikdy nevypisuj.

    if ((Get-FileHash -Algorithm SHA256 -LiteralPath $NewExe).Hash -ne $ExpectedNewHash) { throw 'New executable hash mismatch; STOP.' }
    $newStdout = Join-Path $PrivateDir 'gateway.stdout.log'
    $newStderr = Join-Path $PrivateDir 'gateway.stderr.log'
    Remove-Item -LiteralPath $newStdout,$newStderr -Force -ErrorAction SilentlyContinue
    $NewLaunchAttemptUtc = [DateTime]::UtcNow
    $NewLaunchAttemptId = [guid]::NewGuid().ToString('N')
    $script:C201NewLaunchAttemptUtc = $NewLaunchAttemptUtc
    $script:C201NewLaunchAttemptId = $NewLaunchAttemptId
    $NewLaunchAttempted = $true
    [IO.File]::WriteAllText((Join-Path $BackupRoot 'new-launch-attempt.utc'),$NewLaunchAttemptUtc.ToString('o'),[Text.Encoding]::ASCII)
    $attemptArguments = '-managed -listen ' + $ApiListen + ' -data-dir "' + $DataDir + '"'
    $attemptRecord = [pscustomobject]@{ attempt_id=$NewLaunchAttemptId; started_utc=$NewLaunchAttemptUtc.ToString('o'); started_utc_ticks=[long]$NewLaunchAttemptUtc.Ticks; executable=[IO.Path]::GetFullPath($NewExe); arguments=$attemptArguments; owner_sid=$OwnerSid; data_dir=[IO.Path]::GetFullPath($DataDir) } | ConvertTo-Json -Compress
    [IO.File]::WriteAllText((Join-Path $BackupRoot 'new-launch-attempt.json'),$attemptRecord,[Text.UTF8Encoding]::new($false))
    Set-C201RunbookState 'NewLaunchAttempted'
    $script:C201ActiveLaunchRole = 'new'
    try { $newProcess = Start-C201Gateway $NewExe $newStdout $newStderr }
    finally { $script:C201ActiveLaunchRole = $null }
    if (-not $newProcess -or $newProcess.HasExited) { throw 'New gateway exited during launch; invoke the failure path for the current runbook state.' }
    Set-C201RunbookState 'NewStarted'
    $manifest = Get-Content -Raw -LiteralPath $LaunchFile | ConvertFrom-Json
    $manifest.gateway_pid = $newProcess.Id
    $manifest.gateway_executable = $NewExe
    [IO.File]::WriteAllText($LaunchFile,($manifest | ConvertTo-Json -Depth 10),[Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $PrivateDir 'gateway.pid'),[string]$newProcess.Id,[Text.Encoding]::ASCII)
    $NewPid = $newProcess.Id
    $NewStartUtc = $newProcess.StartTime.ToUniversalTime()
    $NewStartUtcText = $NewStartUtc.ToString('o')
    $startupState = Invoke-C201RuntimeCheck $NewPid $NewExe $NewStartUtcText 'post-start'
    if ($startupState -eq 'empty') {
        # This is the only initializer GET. A timeout is never retried.
        $inventory = Invoke-C201AuthenticatedGet '/v2/cameras'
        $cameraStream = Assert-OneC201Stream $inventory
    } elseif ($startupState -notin @('starting','ready')) { throw 'Unexpected startup state; STOP.' }
    $startupDeadline = [DateTime]::UtcNow.AddSeconds(30)
    while ($startupState -ne 'ready' -and [DateTime]::UtcNow -lt $startupDeadline) {
        Start-Sleep -Seconds 1
        $startupState = Invoke-C201RuntimeCheck $NewPid $NewExe $NewStartUtcText 'post-start'
        if ($startupState -notin @('empty','starting','ready')) { throw 'Ambiguous camera startup state; STOP.' }
    }
    if ($startupState -ne 'ready') { throw 'Timed out waiting for exactly one CS2/TCP session, first_media, RTSP description and fresh inventory; no initializer retry.' }
    if (-not $inventory) {
        # Cache/session were verified ready first. This GET selects the existing RTSP path; verify again immediately afterward.
        $null = Invoke-C201RuntimeCheck $NewPid $NewExe $NewStartUtcText 'pre-inventory'
        $inventory = Invoke-C201AuthenticatedGet '/v2/cameras'
    }
    $cameraStream = Assert-OneC201Stream $inventory
    $null = Invoke-C201RuntimeCheck $NewPid $NewExe $NewStartUtcText 'pre-inventory'
    $info = Invoke-C201AuthenticatedGet '/v2/info'
    if (-not $info.instance_id -or $info.api_version -ne '2.0') { throw 'Managed API identity/version check failed; STOP.' }
    $info = $null
    $inventory = $null

    # Decode from the existing RTSP path only; this does not call Provider.Streams or open CS2.
    # FFmpeg writes no image file; its progress/errors remain private under the selected data-dir.
    if ($cameraStream.RTSPUrl -match '[\r\n"]') { throw 'RTSP URL contains an unsafe argument character; STOP.' }
    $startupStamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ')
    $ffmpegProgress = Join-Path $PrivateDir ('startup-rtsp-progress-' + $startupStamp + '.txt')
    $ffmpegErrors = Join-Path $PrivateDir ('startup-rtsp-' + $startupStamp + '.stderr.log')
    $ffArgs = '-nostdin -hide_banner -loglevel error -progress pipe:1 -nostats -rtsp_transport tcp -i ' + (Quote-C201Arg $cameraStream.RTSPUrl) + ' -map 0:v:0 -an -frames:v 8 -f null NUL'
    $frameCheck = Start-Process -FilePath $VerifiedFfmpegPath -ArgumentList $ffArgs -WorkingDirectory $VerifiedWorkingDirectory -WindowStyle Hidden -PassThru -Wait -RedirectStandardOutput $ffmpegProgress -RedirectStandardError $ffmpegErrors
    $frameRows = @(Get-Content -LiteralPath $ffmpegProgress -ErrorAction Stop | Where-Object { $_ -match '^frame=\d+$' })
    if ($frameCheck.ExitCode -ne 0 -or $frameRows.Count -eq 0 -or $frameRows[-1] -ne 'frame=8') { throw 'RTSP did not decode eight frames; do not continue to PTZ.' }
    $null = Invoke-C201RuntimeCheck $NewPid $NewExe $NewStartUtcText 'pre-inventory'
    Set-C201RunbookState 'NewVerified'
    $cameraStream = $null

## 4. Rollback

Při chybě po OriginalStopped script-level trap zavolá Invoke-C201Rollback. Záměrný rollback po NewVerified spustí stejnou funkci. Funkce načte poslední stav z backup adresáře, zastaví pouze jednoznačně identifikovanou novou instanci přes ověřený procesní handle, ověří neobsazenost DataDir, obnoví state/ACL a soukromé soubory pouze po ověřeném backupu, znovu spustí původní executable a ověří API, inventář a osm RTSP snímků. Před OriginalStopped pouze vrátí kód 1 bez restartu. Úspěšný návrat znamená, že upgrade selhal, ale původní gateway je obnoven; exit code zůstává 1. Chyba obnovy je RecoveryFailed, exit code 20. Opakované zavolání ověří completion marker a původní proces a nic znovu nezapisuje.

    try {
        $rollbackResult = Invoke-C201Rollback
        Write-Output ('C201_ROLLBACK=' + $rollbackResult.state + ' action=' + $rollbackResult.action + ' exit_code=' + $rollbackResult.exit_code)
        exit [int]$rollbackResult.exit_code
    } catch {
        $reason = ([string]$_.Exception.Message -replace '[\r\n]+',' ')
        if ($script:C201RestartedOriginalSnapshot) { $script:C201RecoveryOutcome = Get-C201RestartedProcessOutcome }
        try { Set-C201RunbookState 'RecoveryFailed' } catch {}
        [Console]::Error.WriteLine(('C201_ROLLBACK=FAIL state=RecoveryFailed exit_code=20 actual=' + $script:C201RecoveryOutcome + ' reason=' + $reason))
        exit 20
    }
## 5. Budoucí jediný managed PTZ krok — samostatné schválení

Před testem aktuálně potvrdit fyzickou fázi 0: PTZ funguje v Xiaomi Home, tracking je vypnutý a scéna je statická/bezpečná. Managed helper před Bearer požadavkem ověří proces/listener, před i po inventory GETu kontroluje session, vyžaduje čerstvou inventory cache, právě jednu CS2/TCP session k 192.168.2.124, nulový počet dalších gateway session, first_media a rtsp_description=ready a RTSP socket přiřazený cílovému PID. Pokud se cokoliv nedá ověřit, končí bez POST. Samostatný inventory GET před helperem se nedělá.

Helper používá jednu kontinuální RTSP/TCP relaci a jeden 848×480 FFmpeg dekodér. Vyžaduje nejméně 8 souvislých stabilních výchozích snímků a drift nejvýše 2 px. Nestabilní baseline znamená nula POSTů. Po stabilní baseline provede maximálně jeden autentizovaný POST left a měří přechod/klid ze stejného dekodéru. Chybu ani timeout neopakuje.

    $Probe = 'C:\DEV\xiaomi-local-gateway\.test-env\builds\managed-ptz-handoff\managed-ptz-http-probe.exe'
    $RuntimeScript = 'C:\DEV\xiaomi-local-gateway\.test-env\c201-pilot\win-gateway\managed-ptz-handoff\verify-managed-ptz-runtime.ps1'
    $Ffmpeg = 'C:\DEV\xiaomi-local-gateway\.test-env\c201-pilot\win-gateway\ffmpeg.exe'
    $Evidence = Join-Path $PrivateDir ('managed-ptz-' + [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ') + '.sanitized.log')
    if ((Read-Host 'Aktuálně potvrzuji fyzickou fázi 0; napiš CONFIRMED') -cne 'CONFIRMED') { throw 'Fáze 0 nepotvrzena; žádný požadavek.' }
    if ((Read-Host 'Samostatně schválen jediný živý left krok; napiš ONE-LEFT-APPROVED') -cne 'ONE-LEFT-APPROVED') { throw 'PTZ krok neschválen; žádný požadavek.' }
    $oldPtz = [Environment]::GetEnvironmentVariable('PTZ_WRITE_ENABLED','Process')
    $oldPhase = [Environment]::GetEnvironmentVariable('C201_PHASE0_CONFIRMED','Process')
    try {
        [Environment]::SetEnvironmentVariable('PTZ_WRITE_ENABLED','true','Process')
        [Environment]::SetEnvironmentVariable('C201_PHASE0_CONFIRMED','true','Process')
        & $Probe -api-base ('http://127.0.0.1:' + $ApiPort) -token-file $TokenFile -ffmpeg $Ffmpeg -gateway-log $LogFile -data-dir $DataDir -gateway-pid $NewPid -gateway-start-utc $NewStartUtc.ToString('o') -gateway-exe $NewExe -runtime-check-script $RuntimeScript -camera-address $CameraAddress -confirm-inventory-session-safe -allow-one-left 2>&1 | Tee-Object -FilePath $Evidence
        $probeExit = $LASTEXITCODE
    } finally {
        [Environment]::SetEnvironmentVariable('PTZ_WRITE_ENABLED',$oldPtz,'Process')
        [Environment]::SetEnvironmentVariable('C201_PHASE0_CONFIRMED',$oldPhase,'Process')
    }
    Write-Output ('PROBE_EXIT_CODE=' + $probeExit + ' EVIDENCE=' + $Evidence)

PTZ_DIAG se páruje pouze s gateway.stderr.log daného DataDir a časovým oknem pokusu; parser podporuje časovou předponu Go loggeru a sent_at/reply_cmd_id. Report odděluje HTTP write_status, odpověď 0x113/no_reply/unverified, obrazový posun a fyzické otočení. HTTP 200 znamená pouze write_status=sent; reply_received není důkaz pohybu. Při timeoutu se POST neopakuje.

## 6. Konec E1

Aktuální identita procesu, listenery, session, DPAPI/token runtime a firewall pro novou cestu/port/klienta jsou **UNVERIFIED**. Jejich úspěšné ověření, správný hash, důvěryhodné working directory/argumenty, bezpečná záloha a samostatná schválení jsou povinné před budoucí odstávkou a PTZ testem. Provozní příkazy tohoto runbooku nebyly spuštěny; následné ověření níže použilo pouze syntetické náhrady.

## 7. Offline kontrola pořadí po startu — 2026-11-10

`verify-managed-ptz-runtime.ps1` má read-only fázi `post-start`: ještě před Bearer požadavkem ověří PID, executable, argumenty/data-dir, vlastníka, čas startu, API listener na loopbacku, startovní manifest, dostupnost lokálního DPAPI profilu, přidružené soubory a absenci jiné/nejednoznačné session ke C201. Vrací `empty` (bez session a TCP socketu), `starting` (jedna otevřená session, média/description se ještě připravují) nebo `ready` (jedna session, first_media, RTSP description a čerstvá inventura). Požadavek `/v2/cameras` se smí poslat právě jednou pouze pro `empty`; timeout neznámého výsledku se neopakuje. Stav `starting` pouze polluje. Až `ready` dovolí předběžnou kontrolu inventury a dekódování osmi snímků do `null`; PTZ se v tomto kroku neprovádí.

Offline test používá falešnou identitu procesu/listeneru, náhradu autentizovaného HTTP GETu a falešné stavy C201 session/TCP. Neprovádí síťový požadavek ani kamerovou operaci. Testuje prázdnou cache a úspěšnou inicializaci právě jedním GETem, již připravený stav bez inicializačního GETu, cizí/nejednoznačnou session, selhání identity před Bearerem, timeout přípravy po jednom GETu i timeout samotného GETu bez retry. Další test ověřuje pořadí kroků pro novou i původní binárku v runbooku.

Přímý offline výstup po přidání testů:

```text
go test ./.test-env/c201-pilot/win-gateway/managed-ptz-handoff -count=1 -run '^TestDocumented(StartupGate|RollbackStartupGate)|^TestRunbookOrdersPostStartGateBeforeInitializerAndReadiness' -v
offline_startup=empty_cache identity_before_bearer=true initializer_gets=1 final_cs2=1 media=1 rtsp_ready=1
offline_startup=identity_failure bearer_requests=0 session_checks=0
offline_startup=other_session_detected initializer_gets=0
offline_startup=readiness_timeout initializer_gets=1 retry_gets=0 final_state=starting
offline_startup=already_ready initializer_gets=0
offline_startup=rollback_empty_cache initializer_gets=1 final_cs2=1 media=1 rtsp_ready=1
offline_startup=initializer_http_timeout bearer_gets=1 retry_gets=0
offline_runbook_order=PASS post_start_before_bearer initializer_before_poll ready_gate_before_inventory_and_rtsp
PASS
ok github.com/xiaomi-local-gateway/xiaomi-local-gateway/.test-env/c201-pilot/win-gateway/managed-ptz-handoff 1.558s
STARTUP_OFFLINE_EXIT_CODE=0
```

Závěrečný celý offline balíček byl ověřen příkazem `go test ./.test-env/c201-pilot/win-gateway/managed-ptz-handoff -count=1 -v`, `GOPROXY=off`, `GOSUMDB=off`, `GOFLAGS=-mod=readonly`; výstup končil `PASS`, `ok ... 25.627s`, `FULL_PACKAGE_TEST_EXIT_CODE=0`. Zahrnul mock procesové identity, HTTP GETu i stavů CS2/TCP, souvislý syntetický odběr, stabilní/timeout větve a scénáře startu i rollbacku. PowerShell parser prošel verifier a 12 příkazových bloků runbooku. Žádný běžící proces, firewall, gateway ani kamera nebyly dotčeny.

### Oprava PowerShell chyb po review

- Wrapper runbooku používá parametr `$GatewayProcessId`, aby nekolidoval s automatickou proměnnou `$PID`.
- Verifier po ověření loopback API listeneru načítá TCP tabulku bez filtru a filtruje C201 spojení v paměti. Nula odpovídajících spojení je nyní legitimní `empty`; chyba čtení celé TCP tabulky i cizí vlastník spojení stále fail-closed zastaví.
- Přímý headless běh skutečného verifieru v offline režimu:

```text
& 'C:\Users\Martin\.cache\codex-runtimes\codex-primary-runtime\dependencies\native\powershell\pwsh.exe' -NoLogo -NoProfile -File .test-env/c201-pilot/win-gateway/managed-ptz-handoff/verify-managed-ptz-runtime.ps1 -SelfTest
CIM_STARTTIME_SELFTEST=PASS datetime_value=dmtf_string=PASS
POST_START_SELFTEST=PASS zero_c201_connections=empty ready_session=ready foreign_owner=stop access_error=stop
PWSH_VERIFIER_SELFTEST_EXIT_CODE=0
```

Self-test přepisuje `Get-NetTCPConnection`: vrací pouze falešný API listener pro stav bez spojení, pak simuluje chybu přístupu; nečte hostitelské sockety ani procesy a neposílá HTTP. Windows PowerShell odmítl tentýž běh hláškou `AuthorizationManager check failed`, exit `1`; nepoužil jsem přepnutí execution policy ani jiný způsob obcházení. Ověření runbookového pořadí a mock HTTP/process/session scénářů je oddělené v Go testech uvedených výše.

### Přímý test PowerShell toku startu a rollbacku — 2026-11-10

Přímý headless test načetl funkce `Invoke-C201RuntimeCheck`, `Invoke-C201AuthenticatedGet` a `Assert-OneC201Stream` a úseky runbooku pro nový start a rollback. Child verifier, HTTP, FFmpeg proces i čekání byly nahrazené lokálními syntetickými odpověďmi. Test ověřil předání PID wrapperu, jednorázovou inicializaci při `empty`, následnou připravenost, zastavení před Bearer požadavkem při chybě TCP ověření a žádné opakování při timeoutu GETu. Neotevřel socket ani proces gateway a nedotkl se kamery.

Přímý test odhalil kolizi `$matches` s automatickou proměnnou PowerShellu při `-notmatch`; v `Assert-OneC201Stream` byl tento lokální název změněn na `$c201Matches`.

Příkaz:

```text
& 'C:\Users\Martin\.cache\codex-runtimes\codex-primary-runtime\dependencies\native\powershell\pwsh.exe' -NoLogo -NoProfile -File '.test-env/c201-pilot/win-gateway/managed-ptz-handoff/runbook-startup-selftest.ps1'
```

Přímý výstup:

```text
RUNBOOK_STARTUP_SELFTEST=PASS pid=8101 state=empty initializer_gets=1 ready_poll=1
RUNBOOK_ROLLBACK_SELFTEST=PASS pid=8102 state=empty initializer_gets=1 ready_poll=1
RUNBOOK_TCP_FAILURE_SELFTEST=PASS bearer_requests=0
RUNBOOK_HTTP_TIMEOUT_SELFTEST=PASS initializer_gets=1 retry_gets=0
RUNBOOK_STARTUP_SELFTEST_EXIT_CODE=0
RUNBOOK_SELFTEST_EXIT_CODE=0
```

Návratový kód procesu: `0`. Rollback test pokrývá přímo jeho post-start/readiness gate, nikoli zastavování procesu ani obnovu souborů/ACL; tyto provozní kroky zůstaly neprovedené.

## E1.1 – transakční rollback a offline závěr (2026-11-11)

Sdílené funkce pro stav, cleanup a rollback jsou definované před prvním `Stop-VerifiedProcess`. Stav přechází přes `Preflight`, `BackupReady`, `StopPending`, `OriginalStopped`, `ManagedStateReady`, `NewLaunchAttempted`, `NewStarted`, `NewVerified`, `RolledBack` a `RecoveryFailed`. Zápis další fáze se uloží atomicky před změnou stavu v paměti. Po úspěšném zastavení se procesní potvrzení z ověřeného handle nastaví ještě před zápisem `OriginalStopped`; při chybě tohoto zápisu rollback zjišťuje skutečný stav procesu a obnovuje původní instanci. Obnova binárky ověřuje hash zálohy, bezpečné použití cílové cesty a ACL. Trap hlásí `RolledBack` pouze po ověřené obnově; jinak vrací `RecoveryFailed` s kódem `20`. Před odstávkou se původní proces nerestartuje. Opakovaný cleanup ověřuje completion marker a stav původní instance.

Offline transakční simulace používá dočasné adresáře, falešné procesy, syntetická HTTP/FFmpeg data a osm snímků 848 × 480. Tyto snímky ověřují pouze testovací větev a nejsou důkazem živého videa z C201.

Samostatná ověřovací cesta FFmpeg se před odstávkou kontroluje existencí souboru a `ffmpeg -version`. Hodnota původní proměnné gateway se sleduje odděleně: testuje i doložený případ `ABSENT`, ve kterém se `XLG_FFMPEG_PATH` do prostředí nové ani obnovené gateway nepřidá. Stejná ověřená cesta se předává oběma syntetickým RTSP kontrolám. Fake FFmpeg posuzuje osm syntetických snímků 848 × 480 a drift nejvýše 2 px ještě ve stavu `NewStarted`; teprve potom vrátí `frame=8` runbooku. Nestabilní vstup je odmítnut před `NewVerified`.

Po spuštění původní binárky rollback ihned uchová vrácený `Process` handle. Pokud readiness následně selže, výsledek recovery rozlišuje běžící proces, ukončený proces a stav, který z handle nelze zjistit; chyba vrací `RecoveryFailed`/`20`. Izolované subprocess scénáře používají skutečný extrahovaný runbook `trap`, failure handler i `Invoke-C201Rollback`; každému procesu se měří jeho skutečný exit code. Samostatný child test spouští skutečný blok ručního rollbacku a ověřuje, že po selhání readiness vypíše stav z drženého process handle.

Příkazy:

```powershell
& 'C:\Users\Martin\.cache\codex-runtimes\codex-primary-runtime\dependencies\native\powershell\pwsh.exe' -NoLogo -NoProfile -File '.test-env/c201-pilot/win-gateway/managed-ptz-handoff/runbook-transaction-selftest.ps1'
& 'C:\Users\Martin\.cache\codex-runtimes\codex-primary-runtime\dependencies\native\powershell\pwsh.exe' -NoLogo -NoProfile -File '.test-env/c201-pilot/win-gateway/managed-ptz-handoff/runbook-startup-selftest.ps1'
```

Přímý výstup transakčních scénářů:

```text
RUNBOOK_STATE_SEQUENCE=PASS state=Preflight>BackupReady>StopPending original=running exit=0
SCENARIO_1_PREFLIGHT=PASS exit=0 state=Preflight recovery=no data=preserved env=preserved image_ffmpeg=verified gateway_ffmpeg=ABSENT orphan_processes=0
SCENARIO_2_FAIL_BEFORE_STOP=PASS exit=1 state=BackupReady recovery=no original=running data=preserved orphan_processes=0
C201_ROLLBACK=PASS state=RolledBack exit_code=1
SCENARIO_3_FAIL_AFTER_STOP=PASS exit=1 state=RolledBack recovery=original_restarted_verified data_acl_binary_acl=preserved orphan_processes=0
C201_ROLLBACK=PASS state=RolledBack exit_code=1
SCENARIO_4_NEW_START_FAILURE=PASS exit=1 state=RolledBack recovery=partial_new_stopped data_acl_binary_acl=preserved orphan_processes=0
C201_ROLLBACK=PASS state=RolledBack exit_code=1
SCENARIO_5_READINESS_FAILURE=PASS exit=1 state=RolledBack recovery=restored_verified data_acl_binary_acl=preserved orphan_processes=0
SCENARIO_6_SUCCESS_EIGHT_SYNTHETIC_FRAMES=PASS exit=0 state=NewVerified frames=8 dimensions=848x480 drift_px=1 recovery=no orphan_processes=0
C201_ROLLBACK=PASS state=RolledBack exit_code=1
SCENARIO_7_IMAGE_CHECK_FAILURE=PASS exit=1 state=RolledBack recovery=restored_verified data_acl_binary_acl=preserved orphan_processes=0
C201_ROLLBACK=PASS state=RolledBack exit_code=1
C201_ROLLBACK=ALREADY_COMPLETE state=RolledBack exit_code=1
SCENARIO_8_CLEANUP_TWICE=PASS exit=1 state=RolledBack recovery=second_call_noop data_acl_binary_acl=preserved orphan_processes=0
C201_ROLLBACK=PASS state=RolledBack exit_code=1
SCENARIO_9_STATE_WRITE_FAILURE_AFTER_STOP=PASS exit=1 state=RolledBack recovery=handle_reconciled data_acl_binary_acl=preserved orphan_processes=0
SCENARIO_10_RESTORE_FAILURE=PASS exit=20 state=RecoveryFailed actual=original_stopped_new_stopped_state_acl_unverified orphan_processes=0
SCENARIO_11_ORIGINAL_RESTART_READINESS_FAILURE=PASS exit=20 state=RecoveryFailed actual=original_gateway_running_process_handle_confirmed_readiness_failed new_stopped orphan_processes=0
SCENARIO_12_OTHER_GATEWAY_PROTECTION=PASS exit=20 state=RecoveryFailed other_instance=untouched data=untouched new_stopped orphan_processes=0
RUNBOOK_TRAP_AND_HANDLER_SELFTEST=PASS actual_handler=yes actual_trap=yes rolledback_exit=1 recoveryfailed_exit=20
RUNBOOK_TRANSACTION_SELFTEST=PASS scenarios=12 trap=PASS state_sequence=PASS undefined_functions=0 exit=0
TRANSACTION_SELFTEST_EXIT_CODE=0
```

Přímý výstup startup/readiness simulace:

```text
RUNBOOK_STARTUP_SELFTEST=PASS pid=8101 state=empty initializer_gets=1 ready_poll=1
RUNBOOK_TCP_FAILURE_SELFTEST=PASS bearer_requests=0
RUNBOOK_HTTP_TIMEOUT_SELFTEST=PASS initializer_gets=1 retry_gets=0
RUNBOOK_STARTUP_SELFTEST_EXIT_CODE=0
STARTUP_SELFTEST_EXIT_CODE=0
```

PowerShell parser v pwsh: `VERIFY_RUNTIME_PARSER=PASS errors=0`, `TRANSACTION_SELFTEST_PARSER=PASS errors=0`, `STARTUP_SELFTEST_PARSER=PASS errors=0`, `RUNBOOK_PRE_STOP_BLOCK_PARSER=PASS errors=0`, návratový kód `0`. Transakční harness parsuje definice runbooku před první odstávkou, vykoná sekvenci stavů a ověřuje, že žádná volaná funkce nechybí. Cílená kontrola whitespace prošla; `git diff --check` skončil kódem `0` a vypsal pouze upozornění Git o LF/CRLF u jiných již změněných tracked souborů. V tomto kroku se nezměnil žádný Go zdroj ani gateway binárka.

Finální binárka zůstává na `C:\DEV\xiaomi-local-gateway\.test-env\builds\managed-ptz-finalization-20261109-f\gateway.exe`, SHA-256 `591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0`; přímá kontrola `Get-FileHash -Algorithm SHA256` se shodovala. Runtime identita cílové gateway, listenery, aktivní CS2 relace a účinný firewall zůstávají neověřené. Nasazení, restart, kamera i PTZ zůstaly nedotčené.

### E1.1 doplňující ověření po závěrečném review — 2026-11-11

Ruční rollback nyní při chybě po pokusu restartovat původní gateway volá stejnou `Get-C201RestartedProcessOutcome` funkci jako automatický handler. Před výstupem `RecoveryFailed`/`20` proto uvádí, zda se původní proces drží v běhu podle vráceného handle, skončil, nebo je jeho stav z handle neověřitelný. Izolovaný child test vykonal přímo try/catch blok z runbooku a naměřil exit `20` s `original_gateway=running_process_handle_confirmed; readiness=failed`.

Syntetická náhrada FFmpegu už nemá `FrameMode` přepínač. Zpracuje pouze zadanou sadu snímků; `frame=8`, exit `0` a úspěšná validace vzniknou výhradně pro osm snímků 848×480 s maximálním driftem ≤2 px. Nestabilní syntetická sada vytvořila `frame=5`, exit `1` a spustila rollback ještě před `NewVerified`.

Další zpřísnění svázalo zastavení kandidáta s potvrzením vráceného procesního handle z konkrétního `Start-Process`. Pro novou gateway se před startem uloží unikátní ID a přesný čas pokusu v tick hodnotě; po návratu handle se ověří PID, vlastník SID, executable, celý command line, start time a data-dir. Teprve potom se uloží `new-process-receipt.json`. Rollback smí zastavit jen proces, který se shoduje s touto receipt. Pokud `Start-Process` selže po vytvoření procesu, ale před vrácením/ověřením handle, receipt neexistuje: odpovídající kandidát se ponechá běžet a výsledek je `RecoveryFailed` s kódem `20`. Nezávislý proces se stejnou cestou, argumenty a vlastníkem proto nelze zaměnit za proces spuštěný tímto během.

Scénář `01_preflight` nyní vykonává přímo read-only příkazový blok preflightu z runbooku. Vstupy, procesní handle, listenery, soubory a hash nové binárky jsou izolované náhrady; systémové změny i dotazy na skutečné procesy/sockets jsou nahrazeny. Test ověřuje shodu prostředí, executable, manifestu, managed state, živý stav falešného původního procesu, dva read-only dotazy listeneru a nula startů. Kód skončil `0`, stav zůstal `Preflight`.

Příkaz transakční sady:

```powershell
& 'C:\Users\Martin\.cache\codex-runtimes\codex-primary-runtime\dependencies\native\powershell\pwsh.exe' -NoLogo -NoProfile -File '.test-env/c201-pilot/win-gateway/managed-ptz-handoff/runbook-transaction-selftest.ps1'
```

Přímý výstup izolovaných procesů:

```text
ISOLATED_SCENARIO=01_preflight exit=0 state=Preflight running_ids=1001 data=preserved result=PASS
ISOLATED_SCENARIO=02_before_stop exit=1 state=BackupReady running_ids=1001 data=preserved result=PASS
ISOLATED_SCENARIO=03_after_stop exit=1 state=RolledBack running_ids=1001 data=preserved result=PASS
ISOLATED_SCENARIO=04_new_start_failure exit=20 state=RecoveryFailed running_ids=2002 data=partial_start_untouched result=PASS
ISOLATED_SCENARIO=05_readiness_failure exit=1 state=RolledBack running_ids=1001 data=preserved result=PASS
ISOLATED_SCENARIO=06_success_eight_frames exit=0 state=NewVerified running_ids=2002 data=preserved frames=8 dimensions=848x480 drift_px=1 ffmpeg=frame=8 source=synthetic result=PASS
ISOLATED_SCENARIO=07_image_failure exit=1 state=RolledBack running_ids=1001 data=preserved frames=8 dimensions=848x480 drift_px=50 ffmpeg=frame=5 source=synthetic result=PASS
ISOLATED_SCENARIO=08_cleanup_twice exit=0 state=RolledBack running_ids=1001 data=preserved result=PASS
ISOLATED_SCENARIO=09_state_write_after_stop exit=1 state=RolledBack running_ids=1001 data=preserved result=PASS
ISOLATED_SCENARIO=10_restore_failure exit=20 state=RecoveryFailed running_ids= data=preserved result=PASS
ISOLATED_SCENARIO=11_original_restart_readiness_failure exit=20 state=RecoveryFailed running_ids=1001 data=preserved result=PASS
ISOLATED_SCENARIO=12_other_gateway_protection exit=20 state=RecoveryFailed running_ids=3003 data=protected result=PASS
ISOLATED_SCENARIO=13_unattributed_identical_gateway exit=20 state=RecoveryFailed running_ids=2002 data=protected guard=returned_handle_receipt_required reason_verified=yes result=PASS
ISOLATED_SUBPROCESS_MATRIX=PASS scenarios=13 measured_exit_codes=yes actual_trap=yes actual_handler=yes actual_rollback=yes
MANUAL_ROLLBACK_PROCESS_STATUS=PASS exit=20 actual=original_gateway_running_process_handle_confirmed_readiness_failed
RUNBOOK_STATE_SEQUENCE=PASS state=Preflight>BackupReady>StopPending original=running exit=0
SCENARIO_1_PREFLIGHT=PASS exit=0 state=Preflight recovery=no data=preserved env=preserved image_ffmpeg=verified gateway_ffmpeg=ABSENT orphan_processes=0
SCENARIO_2_FAIL_BEFORE_STOP=PASS exit=1 state=BackupReady recovery=no_restart original=running data=preserved orphan_processes=0
C201_ROLLBACK=PASS state=RolledBack exit_code=1
SCENARIO_3_FAIL_AFTER_STOP=PASS exit=1 state=RolledBack recovery=original_restarted_verified data_acl_binary_acl=preserved orphan_processes=0
C201_ROLLBACK=PASS state=RolledBack exit_code=1
SCENARIO_4_NEW_START_FAILURE=PASS exit=20 state=RecoveryFailed recovery=unattributed_candidate_left_running data=untouched orphan_processes=0
C201_ROLLBACK=PASS state=RolledBack exit_code=1
SCENARIO_5_READINESS_FAILURE=PASS exit=1 state=RolledBack recovery=restored_verified data_acl_binary_acl=preserved orphan_processes=0
C201_ROLLBACK=PASS state=RolledBack exit_code=1
SCENARIO_6_SUCCESS_EIGHT_SYNTHETIC_FRAMES=PASS exit=0 state=NewVerified frames=8 dimensions=848x480 drift_px=1 recovery=no orphan_processes=0
C201_ROLLBACK=PASS state=RolledBack exit_code=1
SCENARIO_7_IMAGE_CHECK_FAILURE=PASS exit=1 state=RolledBack recovery=restored_verified data_acl_binary_acl=preserved orphan_processes=0
C201_ROLLBACK=PASS state=RolledBack exit_code=1
C201_ROLLBACK=ALREADY_COMPLETE state=RolledBack exit_code=1
SCENARIO_8_CLEANUP_TWICE=PASS exit=1 state=RolledBack recovery=second_call_noop data_acl_binary_acl=preserved orphan_processes=0
C201_ROLLBACK=PASS state=RolledBack exit_code=1
SCENARIO_9_STATE_WRITE_FAILURE_AFTER_STOP=PASS exit=1 state=RolledBack recovery=handle_reconciled data_acl_binary_acl=preserved orphan_processes=0
SCENARIO_10_RESTORE_FAILURE=PASS exit=20 state=RecoveryFailed actual=original_stopped_new_stopped_state_acl_unverified orphan_processes=0
SCENARIO_11_ORIGINAL_RESTART_READINESS_FAILURE=PASS exit=20 state=RecoveryFailed actual=original_gateway_running_process_handle_confirmed_readiness_failed new_stopped orphan_processes=0
SCENARIO_12_OTHER_GATEWAY_PROTECTION=PASS exit=20 state=RecoveryFailed other_instance=untouched data=untouched new_stopped orphan_processes=0
TRANSACTION_SELFTEST_EXIT_CODE=0
```

Každý řádek `ISOLATED_SCENARIO` pochází z naměřeného exit code samostatného `pwsh` procesu; scénáře, které vyžadují obnovu, vykonají skutečný trap, handler a rollback. Scénář 13 používá nezávislý falešný proces spuštěný 500 ms před zaznamenaným pokusem, se stejnou cestou, argumenty a SID vlastníka, ale bez receipt z vráceného handle. Test ověřuje konkrétní důvod odmítnutí, stav `RecoveryFailed`/20 a to, že kandidát 2002 zůstane nedotčen. Scénář 4 ověřuje stejně fail-closed výsledek, když částečný start vznikl, ale `Start-Process` vyhodil chybu před návratem handle. Úspěšné obnovy kontrolují obsah a ACL původní binárky a managed state, původní launch kontext, konečný stav falešných procesů, rollback marker a ochranu dat jiné gateway. Selhání obnovy vrací `RecoveryFailed`/20; `RolledBack` se vypíše až po ověřené obnově. Preflight porovnává před a po běhu prostředí, data i procesy. Temp adresáře se odstraní. Syntetické snímky nejsou důkazem obrazu z fyzické C201.

Závěrečná přímá kontrola:

```text
PARSER=PASS file=runbook-transaction-selftest.ps1 errors=0
PARSER=PASS file=runbook-startup-selftest.ps1 errors=0
PARSER=PASS file=verify-managed-ptz-runtime.ps1 errors=0
PARSER_EXIT=0
RUNBOOK_STARTUP_SELFTEST=PASS pid=8101 state=empty initializer_gets=1 ready_poll=1
RUNBOOK_TCP_FAILURE_SELFTEST=PASS bearer_requests=0
RUNBOOK_HTTP_TIMEOUT_SELFTEST=PASS initializer_gets=1 retry_gets=0
STARTUP_SELFTEST_EXIT=0
git diff --check exit=0
targeted trailing whitespace: runbook=0, transaction-test=0
SHA256 gateway.exe=591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0
```

Pracovní strom obsahuje i další dřívější necommitnuté změny; E1.1 upravil pouze tento runbook a lokální `.test-env/.../runbook-transaction-selftest.ps1` harness (ignorovaný pravidlem `.test-env/`). V tomto kroku nebyl změněn Go zdroj ani `gateway.exe`. Gateway, její procesy, síť, kamera a PTZ zůstaly nedotčené.

### E1.1 finální kontrola handle a časové identity — 2026-11-11

`Start-C201Gateway` teď uloží objekt `Process`, vrácený `Start-Process`, do script scope ihned po návratu; před kontrolou identity nebo zápisem receipt uloží i vazbu na ID pokusu, PID, cestu, argumenty, vlastníka, data-dir a přesný start time. Když trvalý zápis receipt selže, živý běh může bezpečně použít tuto vazbu a stejný handle pro cleanup. Po pádu nebo novém spuštění PowerShellu bez handle rollback proces nezastaví, pokud chybí receipt.

`Stop-VerifiedProcess` přijímá už otevřený handle a pro novou gateway jej znovu neotevírá podle PID. Bezprostředně před `Kill()` obnoví procesní údaje, ověří vlastníka, cestu, úplné argumenty, start time z handle i `CreationDate` aktuálního CIM řádku přesně proti receipt a pak volá `Kill()` na témže handle. Tím PID znovupoužitý mezi kontrolou a zastavením nemůže přesměrovat zásah na jiný proces. Při změně identity nebo času se nic nezastaví a handler vrátí `RecoveryFailed`/`20`.

Nová izolovaná sada obsahuje šestnáct samostatných `pwsh` procesů. Tři přidané případy doplňují původní scénáře:

```text
ISOLATED_SCENARIO=14_start_failure_no_process exit=1 state=RolledBack running_ids=1001 data=preserved result=PASS
ISOLATED_SCENARIO=15_receipt_write_failure_after_handle exit=1 state=RolledBack running_ids=1001 data=preserved binding=returned_handle_cleanup_verified result=PASS
ISOLATED_SCENARIO=16_pid_reuse_before_stop exit=20 state=RecoveryFailed running_ids=2002 data=preserved guard=exact_creation_time_and_bound_handle reason_verified=yes result=PASS
ISOLATED_SUBPROCESS_MATRIX=PASS scenarios=16 measured_exit_codes=yes actual_trap=yes actual_handler=yes actual_rollback=yes
RUNBOOK_TRANSACTION_SELFTEST=PASS scenarios=12 trap=PASS state_sequence=PASS undefined_functions=0 exit=0
TRANSACTION_SELFTEST_EXIT_CODE=0
```

Případ 14 potvrzuje rollback po selhání startu bez vzniku nového procesu. Případ 15 vytvoří proces a vrátí handle, ale úmyslně zablokuje zápis receipt; rollback použije uložený handle, zastaví právě tento simulovaný proces a ověří návrat původního stavu a ACL. Případ 16 znovu použije PID s novým časem vytvoření až při poslední kontrolní dotazu před `Kill`; výstup je `RecoveryFailed`/`20`, nový proces 2002 zůstane běžet a data se nemění. Žádný test nevytvořil osiřelý falešný proces.

Ověření bylo offline. Syntetické procesy a snímky nejsou důkazem živého běhu gateway ani videa C201. Nebyl změněn Go zdroj, finální binárka, skutečný proces, firewall, kamera ani PTZ.

Závěrečné opakované kontroly:

```text
PARSER=PASS file=runbook-transaction-selftest.ps1 errors=0
PARSER=PASS file=runbook-startup-selftest.ps1 errors=0
PARSER=PASS file=verify-managed-ptz-runtime.ps1 errors=0
PARSER_EXIT_CODE=0
RUNBOOK_STARTUP_SELFTEST=PASS pid=8101 state=empty initializer_gets=1 ready_poll=1
RUNBOOK_TCP_FAILURE_SELFTEST=PASS bearer_requests=0
RUNBOOK_HTTP_TIMEOUT_SELFTEST=PASS initializer_gets=1 retry_gets=0
STARTUP_SELFTEST_EXIT_CODE=0
TARGETED_TRAILING_WHITESPACE=PASS
GIT_DIFF_CHECK_EXIT_CODE=0
GATEWAY_SHA256=591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0
```

Parser příkazů a transakční simulace extrahuje a parsuje i PowerShell funkce a skutečné preflight/start/rollback bloky z tohoto runbooku. Kontrola `git diff --check` vypsala jen upozornění Git o LF/CRLF u jiných dříve změněných tracked souborů; návratový kód byl `0`.

### E1.1 doplnění: přechodná chyba první CIM kontroly — 2026-11-11

Pokud se první CIM dotaz po návratu `Start-Process` nezdaří, vrácený `Process` handle a jeho start time zůstávají uložené ještě před dotazem. Rollback rozpozná neúplné `creation_utc_ticks` a funkcí `Complete-C201ReturnedProcessBinding` znovu ověří konkrétní PID proti témuž handle: PID, neukončený stav, přesný handle start time, executable, command line, vlastníka SID a CIM `CreationDate`. Po úspěchu doplní chybějící CIM čas, dále použije tentýž handle a provede běžnou obnovu. Opakovaná chyba nebo jakákoli nejednoznačnost končí `RecoveryFailed`/`20` bez zastavení kandidáta a bez obnovy souborů.

Přidaný případ `17_transient_initial_cim_failure` injektuje jednu chybu do prvního filtrovaného CIM dotazu mezi návratem handle a uložením receipt. Skutečný runbook trap následně identitu jednou ověří znovu, zastaví novou simulovanou gateway přes uložený handle a vrátí původní proces i zálohovaný obsah/ACL:

```text
ISOLATED_SCENARIO=17_transient_initial_cim_failure exit=1 state=RolledBack running_ids=1001 data=preserved recovery=same_handle_reverified identity_query_retries=1 result=PASS
ISOLATED_SUBPROCESS_MATRIX=PASS scenarios=17 measured_exit_codes=yes actual_trap=yes actual_handler=yes actual_rollback=yes
RUNBOOK_TRANSACTION_SELFTEST=PASS scenarios=12 trap=PASS state_sequence=PASS undefined_functions=0 exit=0
TRANSACTION_SELFTEST_EXIT_CODE=0
PARSER_EXIT_CODE=0
STARTUP_SELFTEST_EXIT_CODE=0
GIT_DIFF_CHECK_EXIT_CODE=0
GATEWAY_SHA256=591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0
```

Ověřená znovupoužitá PID (scénář 16) nadále končí kódem `20` a ponechá náhradní proces běžet; kandidát bez receipt i bez in-memory handle (scénář 13) také končí `20` beze změny procesu či jeho dat. Všechny kontroly zůstaly offline a syntetické; nedošlo ke změně Go zdrojů, gateway binárky, skutečných procesů, sítě, kamery ani PTZ.

### E1.1 doplnění: nová instance skončí před prvním CIM ověřením — 2026-11-11

Pokud uložený `Process` handle již hlásí ukončení, `Complete-C201ReturnedProcessBinding` provede cílený read-only dotaz na jeho PID. Pouze nulový počet řádků dovolí označit `exited_confirmed`; obsazený/recyklovaný PID nebo nečitelný dotaz končí `RecoveryFailed`/`20`. Před obnovením souborů rollback stále provede úplný soupis gateway procesů a `Assert-DataDirUnused`; jiná instance používající data-dir tedy zablokuje obnovu. Ukončený proces se nezastavuje, protože není co zastavovat.

Scénář 18 simuluje, že `Start-Process` vrátí objekt, ale gateway skončí před prvním úspěšným CIM ověřením. Handle potvrzuje konec, PID není obsazený, data-dir je volný, takže se obnoví původní proces, managed state a ACL. Scénáře 13 a 16 dál ověřují fail-closed ochranu neověřeného procesu a PID reuse.

```text
ISOLATED_SCENARIO=18_process_exits_before_identity_cim exit=1 state=RolledBack running_ids=1001 data=preserved recovery=handle_exited_pid_unoccupied data_dir_rechecked=yes result=PASS
ISOLATED_SCENARIO=17_transient_initial_cim_failure exit=1 state=RolledBack running_ids=1001 data=preserved recovery=same_handle_reverified identity_query_retries=1 result=PASS
ISOLATED_SCENARIO=13_unattributed_identical_gateway exit=20 state=RecoveryFailed running_ids=2002 data=protected guard=returned_handle_receipt_required reason_verified=yes result=PASS
ISOLATED_SCENARIO=16_pid_reuse_before_stop exit=20 state=RecoveryFailed running_ids=2002 data=preserved guard=exact_creation_time_and_bound_handle reason_verified=yes result=PASS
ISOLATED_SUBPROCESS_MATRIX=PASS scenarios=18 measured_exit_codes=yes actual_trap=yes actual_handler=yes actual_rollback=yes
RUNBOOK_TRANSACTION_SELFTEST=PASS scenarios=12 trap=PASS state_sequence=PASS undefined_functions=0 exit=0
TRANSACTION_SELFTEST_EXIT_CODE=0
```

Tímto pokračováním se testovací matice rozšířila z 17 na 18 izolovaných podprocesů. Změněn byl pouze runbook a ignorovaný transakční test harness; Go zdroje a finální `gateway.exe` zůstaly beze změny.

### E1.1 doplnění: obnovení procesního soupisu po potvrzeném ukončení — 2026-11-11

Rollback nejprve získá procesní soupis pro kandidáty. Pokud kontrola vráceného handle následně potvrdí, že nová gateway skončila a cílený CIM dotaz už její PID nevidí, původní soupis může být zastaralý. Runbook jej nyní zahodí, znovu načte `Win32_Process` a před obnovou ověří, že PID stále není obsazený. Chyba nového soupisu, opětovné obsazení PID nebo jiný proces používající `data-dir` nadále zastaví obnovu jako `RecoveryFailed`/`20`.

Nový izolovaný scénář 19 nejprve vyvolá přechodnou chybu identity CIM, takže první rollback soupis ještě obsahuje novou gateway; další cílený CIM dotaz ukončí simulovaný proces. Test kontroluje přechod soupisu z PID `2002` na jeho nepřítomnost a ověřuje obnovu původního procesu, souborů a ACL. Výstup následuje:

```text
ISOLATED_SCENARIO=19_stale_inventory_refreshed_after_exit exit=1 state=RolledBack running_ids=1001 data=preserved recovery=fresh_inventory_after_exit stale_rows_discarded=yes result=PASS
ISOLATED_SUBPROCESS_MATRIX=PASS scenarios=19 measured_exit_codes=yes actual_trap=yes actual_handler=yes actual_rollback=yes
RUNBOOK_TRANSACTION_SELFTEST=PASS scenarios=12 trap=PASS state_sequence=PASS undefined_functions=0 exit=0
TRANSACTION_SELFTEST_EXIT_CODE=0
PARSER_EXIT_CODE=0
STARTUP_SELFTEST_EXIT_CODE=0
GIT_DIFF_CHECK_EXIT_CODE=0
GATEWAY_SHA256=591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0
```

Scénáře 13 a 16 nadále vracejí `20` a ponechávají neověřený nebo recyklovaný proces nedotčený; scénáře 17 a 18 nadále vracejí `1` po bezpečném rollbacku. Běh byl čistě offline se syntetickými procesy. Nezměnily se Go zdroje ani finální binárka a nebyly ovlivněny skutečné gateway procesy, kamera, firewall ani PTZ.

### E1.1 doplnění: gateway skončí po uložení startovní identity — 2026-11-11

Rollback nyní umí reconciliovat i platný startovní receipt, pokud jeho svázaný `Process` handle během kontroly soupisu zjistí, že nová gateway už skončila. Receipt se nejprve porovná s údaji téhož handle; poté se cíleným CIM dotazem potvrdí, že PID není recyklovaný, a načte se nový úplný soupis. Obnova pokračuje jen pokud je PID stále volný a následná kontrola potvrdí, že `data-dir` nepoužívá jiná gateway. Handle zůstává otevřený až do dokončení poslední kontroly zastavení, takže lze stejně bezpečně zpracovat i konec procesu těsně před ní. Nečitelná identita, obsazený PID nebo cizí proces nadále znamenají `RecoveryFailed`/`20` bez zastavení kandidáta či obnovy souborů.

Scénář 20 nechá novou gateway projít startem a syntetickou osmiframovou kontrolou, ověří, že receipt už existuje, a teprve potom ji ukončí při čtení vlastníka z rollbackového soupisu. Ověřuje se přechod čerstvého soupisu z PID `2002` na jeho nepřítomnost, původní proces `1001`, obnovený obsah i ACL a nulový počet osiřelých simulovaných procesů:

```text
ISOLATED_SCENARIO=20_receipted_process_exits_during_rollback_scan exit=1 state=RolledBack running_ids=1001 data=preserved recovery=receipt_matched_handle_exit_reconciled stale_rows_discarded=yes result=PASS
ISOLATED_SUBPROCESS_MATRIX=PASS scenarios=20 measured_exit_codes=yes actual_trap=yes actual_handler=yes actual_rollback=yes
RUNBOOK_TRANSACTION_SELFTEST=PASS scenarios=12 trap=PASS state_sequence=PASS undefined_functions=0 exit=0
TRANSACTION_SELFTEST_EXIT_CODE=0
RUNBOOK_STARTUP_SELFTEST=PASS pid=8101 state=empty initializer_gets=1 ready_poll=1
RUNBOOK_TCP_FAILURE_SELFTEST=PASS bearer_requests=0
RUNBOOK_HTTP_TIMEOUT_SELFTEST=PASS initializer_gets=1 retry_gets=0
STARTUP_SELFTEST_EXIT_CODE=0
POWERSHELL_PARSER_EXIT_CODE=0
GATEWAY_SHA256=591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0
```

Scénáře 17–19 zůstaly `RolledBack`/`1`; scénáře 13 a 16 zůstaly fail-closed jako `RecoveryFailed`/`20`. Všechny snímky a procesy byly syntetické. Nebyla spuštěna skutečná gateway ani kamera, změněn firewall nebo odeslán PTZ příkaz; Go zdroje a finální `gateway.exe` zůstaly beze změny.

### E1.2 doplnění: selhání `GetOwner()` po ukončení gateway — 2026-11-11

Při výjimce nebo nenulovém `ReturnValue` z `GetOwner()` rollback nepovažuje neúspěch dotazu za důkaz konce procesu. Pokračuje pouze tehdy, pokud uložený receipt souhlasí s původním handle, handle potvrzuje ukončení, cílený CIM dotaz a nový úplný soupis potvrzují volný PID a následné kontroly potvrdí výlučný přístup k `data-dir` i původní binárce. Selhání `GetOwner()` u stále běžící instance končí `RecoveryFailed`/`20` a ponechává ji nedotčenou.

Izolované varianty 20a a 20b ukončí novou instanci po pořízení zastaralého rollbackového soupisu a simulují výjimku, respektive nenulový výsledek `GetOwner()`. Obě obnoví pouze původní simulovaný proces a ověří obsah i ACL oproti záloze. Varianta 20c potvrzuje, že stejná chyba při stále běžící instanci neumožní obnovu ani zastavení kandidáta.

```text
ISOLATED_SCENARIO=20a_getowner_exception_after_exit exit=1 state=RolledBack running_ids=1001 data=preserved recovery=GetOwner_failure_handle_exit_confirmed_pid_free=yes result=PASS
ISOLATED_SCENARIO=20b_getowner_nonzero_after_exit exit=1 state=RolledBack running_ids=1001 data=preserved recovery=GetOwner_failure_handle_exit_confirmed_pid_free=yes result=PASS
ISOLATED_SCENARIO=20c_getowner_exception_while_alive exit=20 state=RecoveryFailed running_ids=2002 data=preserved guard=GetOwner_failure_not_exit_evidence candidate_left_running=yes result=PASS
ISOLATED_SUBPROCESS_MATRIX=PASS scenarios=23 measured_exit_codes=yes actual_trap=yes actual_handler=yes actual_rollback=yes
RUNBOOK_TRANSACTION_SELFTEST=PASS scenarios=12 trap=PASS state_sequence=PASS undefined_functions=0 exit=0
TRANSACTION_SELFTEST_EXIT_CODE=0
RUNBOOK_STARTUP_SELFTEST=PASS pid=8101 state=empty initializer_gets=1 ready_poll=1
RUNBOOK_TCP_FAILURE_SELFTEST=PASS bearer_requests=0
RUNBOOK_HTTP_TIMEOUT_SELFTEST=PASS initializer_gets=1 retry_gets=0
STARTUP_SELFTEST_EXIT_CODE=0
POWERSHELL_PARSER_EXIT_CODE=0
GIT_DIFF_CHECK_EXIT_CODE=0
GATEWAY_SHA256=591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0
```

Scénář 20c doplňuje povinný negativní důkaz k variantám 20a/20b: chyba `GetOwner()` sama nestačí. Ostatní ochranné případy pro cizí proces, neověřenou identitu a PID reuse zůstaly ve stejné matici a nadále selhávají uzavřeně. Kontrola ACL v neúspěšném scénáři 10 zaznamenává injekci chyby `Set-Acl` místo spoléhání na změnu testovacího ACL, kterou toto omezené PowerShell prostředí nepersistuje; stav se tam správně hlásí jako `state_acl_unverified`. Úspěšné rollback varianty stále porovnávají obnovené ACL se zálohou. Všechny testy byly offline a syntetické; žádné Go zdroje, skutečné procesy, kamera, firewall, PTZ ani binárka nebyly změněny.

### E1.2 doplnění: předběžná validace záloh rollbacku — 2026-11-11

Před skenováním či zastavením náhradního procesu runbook nyní ověří kompletní sadu souborů potřebných k obnově. Každá záloha vytvořená přes `Save-BackupFile` má SHA-256 sidecar a čitelný, parsovatelný záznam ACL; kontrola porovná hash se skutečným obsahem. Zvlášť se ověří kontrolní soubor původní binárky, úplnost a soulad identity `target-process.json`, integrita snímku `pre-stop-state.json`, povinné soukromé soubory a případná záloha `managed-state.json` nebo jednoznačný marker jeho původní absence. Obnova souborů začíná až po této read-only validaci. Chybějící nebo poškozená záloha vede k `RecoveryFailed`/`20`, bez zastavení kandidáta, zápisu či smazání původních cílových souborů a bez spuštění původní gateway. Záloha `managed-state.json` se ověřuje podle svého konzistentního snímku po odstavení, ne proti dřívějšímu snímku z doby, kdy gateway ještě běžela.

Nový izolovaný scénář 21 navazuje na úspěšný start a na podmínky 20a: nová simulovaná gateway samovolně skončí, před rollbackem se odstraní kontrolní soubor původní binárky. Runbook vrátí `RecoveryFailed`/`20`; test porovná přítomnost, SHA-256 a ACL původní binárky, `managed-state.json` a soukromých souborů před a po rollbacku. Všechna data zůstanou beze změny a původní proces se nespustí. Varianty 20a/20b dál ověřují bezpečný rollback při výjimce a nenulovém `GetOwner()` pouze po potvrzení konce přes původní handle a čerstvé kontroly PID.

Přímý výstup cílené matice a závěrečných kontrol:

```text
ISOLATED_SCENARIO=20a_getowner_exception_after_exit exit=1 state=RolledBack running_ids=1001 data=preserved recovery=GetOwner_failure_handle_exit_confirmed_pid_free=yes result=PASS
ISOLATED_SCENARIO=20b_getowner_nonzero_after_exit exit=1 state=RolledBack running_ids=1001 data=preserved recovery=GetOwner_failure_handle_exit_confirmed_pid_free=yes result=PASS
ISOLATED_SCENARIO=20c_getowner_exception_while_alive exit=20 state=RecoveryFailed running_ids=2002 data=preserved guard=GetOwner_failure_not_exit_evidence candidate_left_running=yes result=PASS
ISOLATED_SCENARIO=21_missing_original_binary_checksum_before_restore exit=20 state=RecoveryFailed running_ids= data=preserved guard=prevalidation_before_process_stop_and_restore=yes target_content_acl_unchanged=yes result=PASS
ISOLATED_SUBPROCESS_MATRIX=PASS scenarios=24 measured_exit_codes=yes actual_trap=yes actual_handler=yes actual_rollback=yes
RUNBOOK_TRANSACTION_SELFTEST=PASS scenarios=12 trap=PASS state_sequence=PASS undefined_functions=0 exit=0
TRANSACTION_SELFTEST_EXIT_CODE=0
PARSER=PASS file=runbook-transaction-selftest.ps1 errors=0
PARSER=PASS file=runbook-startup-selftest.ps1 errors=0
PARSER=PASS file=verify-managed-ptz-runtime.ps1 errors=0
POWERSHELL_PARSER_EXIT_CODE=0
RUNBOOK_STARTUP_SELFTEST=PASS pid=8101 state=empty initializer_gets=1 ready_poll=1
RUNBOOK_TCP_FAILURE_SELFTEST=PASS bearer_requests=0
RUNBOOK_HTTP_TIMEOUT_SELFTEST=PASS initializer_gets=1 retry_gets=0
STARTUP_SELFTEST_EXIT_CODE=0
GIT_DIFF_CHECK_EXIT_CODE=0
GATEWAY_SHA256=591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0
GATEWAY_SHA256_MATCH=PASS
GATEWAY_SHA256_EXIT_CODE=0
```

Matice proběhla v 24 izolovaných PowerShell podprocesech a 12 interních případech. Chráněné scénáře PID reuse, cizí proces/data-dir a neověřená identita dál končí `RecoveryFailed`/`20`; úspěšné rollbacky ověřují návrat obsahu a ACL a nulový počet osiřelých simulovaných procesů. Syntetický úspěch osmi snímků `848x480` je pouze offline test, nikoli důkaz živého obrazu C201. `git diff --check` vrátil 0; Git vypsal pouze upozornění na převod LF/CRLF v jiných dříve změněných souborech. Nebyl změněn Go zdroj ani finální gateway binárka; skutečné procesy, kamera, firewall a PTZ zůstaly nedotčené.

### E1.2 doplnění: časné selhání `GetOwner()` během opakované identity kontroly — 2026-11-11

Při dokončování vazby po přechodném selhání prvního CIM dotazu runbook nejprve ověří PID, executable, úplný command line a čas procesu proti handle vrácenému `Start-Process`. Pokud následný `GetOwner()` vyhodí výjimku nebo vrátí nenulový kód, samotná chyba se nepovažuje za důkaz ukončení. Obnova pokračuje pouze tehdy, když stejný handle potvrzuje ukončení, cílený nový CIM dotaz potvrzuje volný PID a úplný čerstvý soupis jej také nepotvrdí. Následují stávající kontroly `data-dir`, původní binárky a předem ověřených záloh. Živý handle, recyklovaný PID nebo chyba/nejasnost v kterékoliv kontrole zastaví rollback jako `RecoveryFailed`/`20`.

Varianty 17a a 17b vycházejí ze scénáře 17: první CIM identifikace po startu jednou selže a při opakovaném ověření v rollbacku nová simulovaná instance skončí během `GetOwner()`. Testy pokrývají výjimku i nenulový `ReturnValue`; obě varianty zkontrolují čerstvý procesní soupis, obnovený původní proces a obsah/ACL podle záloh. Starší varianty 20a/20b, negativní 20c a scénář chybějící zálohy 21 zůstaly beze změny.

Přímý výstup nových variant a finálních kontrol:

```text
ISOLATED_SCENARIO=17a_transient_cim_then_getowner_exception_after_exit exit=1 state=RolledBack running_ids=1001 data=preserved recovery=initial_cim_failed; GetOwner_failed_during_retry; bound_handle_exit_and_fresh_pid_clear=yes result=PASS
ISOLATED_SCENARIO=17b_transient_cim_then_getowner_nonzero_after_exit exit=1 state=RolledBack running_ids=1001 data=preserved recovery=initial_cim_failed; GetOwner_failed_during_retry; bound_handle_exit_and_fresh_pid_clear=yes result=PASS
ISOLATED_SCENARIO=20a_getowner_exception_after_exit exit=1 state=RolledBack running_ids=1001 data=preserved recovery=GetOwner_failure_handle_exit_confirmed_pid_free=yes result=PASS
ISOLATED_SCENARIO=20b_getowner_nonzero_after_exit exit=1 state=RolledBack running_ids=1001 data=preserved recovery=GetOwner_failure_handle_exit_confirmed_pid_free=yes result=PASS
ISOLATED_SCENARIO=20c_getowner_exception_while_alive exit=20 state=RecoveryFailed running_ids=2002 data=preserved guard=GetOwner_failure_not_exit_evidence candidate_left_running=yes result=PASS
ISOLATED_SCENARIO=21_missing_original_binary_checksum_before_restore exit=20 state=RecoveryFailed running_ids= data=preserved guard=prevalidation_before_process_stop_and_restore=yes target_content_acl_unchanged=yes result=PASS
ISOLATED_SUBPROCESS_MATRIX=PASS scenarios=26 measured_exit_codes=yes actual_trap=yes actual_handler=yes actual_rollback=yes
RUNBOOK_TRANSACTION_SELFTEST=PASS scenarios=12 trap=PASS state_sequence=PASS undefined_functions=0 exit=0
TRANSACTION_SELFTEST_EXIT_CODE=0
PARSER=PASS file=runbook-transaction-selftest.ps1 errors=0
PARSER=PASS file=runbook-startup-selftest.ps1 errors=0
PARSER=PASS file=verify-managed-ptz-runtime.ps1 errors=0
POWERSHELL_PARSER_EXIT_CODE=0
RUNBOOK_STARTUP_SELFTEST=PASS pid=8101 state=empty initializer_gets=1 ready_poll=1
RUNBOOK_TCP_FAILURE_SELFTEST=PASS bearer_requests=0
RUNBOOK_HTTP_TIMEOUT_SELFTEST=PASS initializer_gets=1 retry_gets=0
STARTUP_SELFTEST_EXIT_CODE=0
GIT_DIFF_CHECK_EXIT_CODE=0
GATEWAY_SHA256=591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0
GATEWAY_SHA256_MATCH=PASS
GATEWAY_SHA256_EXIT_CODE=0
```

Transakční matice nyní obsahuje 26 izolovaných procesních scénářů a 12 interních kontrol. Výsledek zůstává čistě offline: syntetické procesy a snímky nejsou důkazem živé C201. Go zdroje ani `gateway.exe` nebyly změněny; nenasazovalo se, nezasahovalo do skutečných procesů, kamery, firewallu ani PTZ. Další rozšiřování matice zde končí.

### E1.2 doplnění: časné selhání `GetOwner()` během opakované identity kontroly — 2026-11-11

Při dokončování vazby po přechodném selhání prvního CIM dotazu runbook nejprve ověří PID, executable, úplný command line a čas procesu proti handle vrácenému `Start-Process`. Pokud následný `GetOwner()` vyhodí výjimku nebo vrátí nenulový kód, samotná chyba se nepovažuje za důkaz ukončení. Obnova pokračuje pouze tehdy, když stejný handle potvrzuje ukončení, cílený nový CIM dotaz potvrzuje volný PID a úplný čerstvý soupis jej také nepotvrdí. Následují stávající kontroly `data-dir`, původní binárky a předem ověřených záloh. Živý handle, recyklovaný PID nebo chyba/nejasnost v kterékoliv kontrole zastaví rollback jako `RecoveryFailed`/`20`.

Varianty 17a a 17b vycházejí ze scénáře 17: první CIM identifikace po startu jednou selže a při opakovaném ověření v rollbacku nová simulovaná instance skončí během `GetOwner()`. Testy pokrývají výjimku i nenulový `ReturnValue`; obě varianty ověřují čerstvý procesní soupis, spuštění pouze původní simulované gateway a shodu obsahu i ACL managed-state a soukromých souborů se zálohami. Varianty 20a/20b dále procházejí; 20c i neplatná záloha 21 zůstávají fail-closed.

Přímý výstup nových variant a finálních kontrol:

```text
ISOLATED_SCENARIO=17a_transient_cim_then_getowner_exception_after_exit exit=1 state=RolledBack running_ids=1001 data=preserved recovery=initial_cim_failed; GetOwner_failed_during_retry; bound_handle_exit_and_fresh_pid_clear=yes restored_state_and_private_acl=yes result=PASS
ISOLATED_SCENARIO=17b_transient_cim_then_getowner_nonzero_after_exit exit=1 state=RolledBack running_ids=1001 data=preserved recovery=initial_cim_failed; GetOwner_failed_during_retry; bound_handle_exit_and_fresh_pid_clear=yes restored_state_and_private_acl=yes result=PASS
ISOLATED_SCENARIO=20a_getowner_exception_after_exit exit=1 state=RolledBack running_ids=1001 data=preserved recovery=GetOwner_failure_handle_exit_confirmed_pid_free=yes restored_state_and_private_acl=yes result=PASS
ISOLATED_SCENARIO=20b_getowner_nonzero_after_exit exit=1 state=RolledBack running_ids=1001 data=preserved recovery=GetOwner_failure_handle_exit_confirmed_pid_free=yes restored_state_and_private_acl=yes result=PASS
ISOLATED_SCENARIO=20c_getowner_exception_while_alive exit=20 state=RecoveryFailed running_ids=2002 data=preserved guard=GetOwner_failure_not_exit_evidence candidate_left_running=yes result=PASS
ISOLATED_SCENARIO=21_missing_original_binary_checksum_before_restore exit=20 state=RecoveryFailed running_ids= data=preserved guard=prevalidation_before_process_stop_and_restore=yes target_content_acl_unchanged=yes result=PASS
ISOLATED_SUBPROCESS_MATRIX=PASS scenarios=26 measured_exit_codes=yes actual_trap=yes actual_handler=yes actual_rollback=yes
RUNBOOK_TRANSACTION_SELFTEST=PASS scenarios=12 trap=PASS state_sequence=PASS undefined_functions=0 exit=0
TRANSACTION_SELFTEST_EXIT_CODE=0
PARSER=PASS file=runbook-transaction-selftest.ps1 errors=0
PARSER=PASS file=runbook-startup-selftest.ps1 errors=0
PARSER=PASS file=verify-managed-ptz-runtime.ps1 errors=0
POWERSHELL_PARSER_EXIT_CODE=0
RUNBOOK_STARTUP_SELFTEST=PASS pid=8101 state=empty initializer_gets=1 ready_poll=1
RUNBOOK_TCP_FAILURE_SELFTEST=PASS bearer_requests=0
RUNBOOK_HTTP_TIMEOUT_SELFTEST=PASS initializer_gets=1 retry_gets=0
STARTUP_SELFTEST_EXIT_CODE=0
GIT_DIFF_CHECK_EXIT_CODE=0
GATEWAY_SHA256=591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0
GATEWAY_SHA256_MATCH=PASS
GATEWAY_SHA256_EXIT_CODE=0
```

`git diff --check` vypisuje pouze dřívější upozornění Git na LF/CRLF a končí kódem 0. Ověření zůstalo offline; Go zdroje, gateway binárka, skutečné procesy, kamera, firewall ani PTZ nebyly změněny.

## E1.2.1 – normalizace `data-dir` s koncovým oddělovačem

`Assert-DataDirUnused` nyní obě cesty převádí přes `GetFullPath` a odstraňuje koncové oddělovače pouze za hranicí kořene cesty. Kořen disku i UNC share root si zachovají oddělovač; neabsolutní nebo prázdná cesta nadále končí chybou a neznámým použitím adresáře.

Nový scénář `20d` vychází z `20a`: po ukončení nové instance při selhání `GetOwner()` se objeví běžící cizí gateway s jiným executable a stejným `data-dir`, zapsaným s koncovým oddělovačem. Rollback skončí `RecoveryFailed`/`20` před změnou souborů. Cizí proces zůstane jediný běžící a kontrola potvrdí nezměněný obsah i ACL. Scénář také ověřuje zachování kořenové cesty. Všechny obrazové vstupy jsou syntetické.

Přímý výstup nové regrese a souhrn kontrol:

```text
ISOLATED_SCENARIO=20d_other_gateway_trailing_separator_data_dir exit=20 state=RecoveryFailed running_ids=3004 data=protected guard=trailing_separator_normalized=yes foreign_process_preserved=yes original_not_restarted=yes target_content_acl_unchanged=yes root_path_preserved=yes result=PASS
ISOLATED_SUBPROCESS_MATRIX=PASS scenarios=27 measured_exit_codes=yes actual_trap=yes actual_handler=yes actual_rollback=yes
TRANSACTION_SELFTEST_EXIT_CODE=0
PARSER=PASS file=runbook-transaction-selftest.ps1 errors=0
PARSER=PASS file=runbook-startup-selftest.ps1 errors=0
PARSER=PASS file=verify-managed-ptz-runtime.ps1 errors=0
POWERSHELL_PARSER_EXIT_CODE=0
RUNBOOK_STARTUP_SELFTEST=PASS pid=8101 state=empty initializer_gets=1 ready_poll=1
RUNBOOK_TCP_FAILURE_SELFTEST=PASS bearer_requests=0
RUNBOOK_HTTP_TIMEOUT_SELFTEST=PASS initializer_gets=1 retry_gets=0
STARTUP_SELFTEST_EXIT_CODE=0
GIT_DIFF_CHECK_EXIT_CODE=0
GATEWAY_PATH=C:\DEV\xiaomi-local-gateway\.test-env\builds\managed-ptz-finalization-20261109-f\gateway.exe
GATEWAY_SHA256=591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0
GATEWAY_SHA256_MATCH=PASS
GATEWAY_SHA256_EXIT_CODE=0
```

`git diff --check` skončil kódem 0; jeho výstup obsahoval jen upozornění na LF/CRLF. Aktualizace se týká pouze runbooku a offline transakčního testu. Go zdroje, gateway binárka a skutečné runtime prostředí zůstaly beze změny.

## E1.2.2 – odmítnutí neplně kvalifikovaného `data-dir`

Před `GetFullPath()` runbook nyní vyžaduje plně kvalifikovanou cestu: absolutní cestu s písmenem disku, UNC share nebo jejich rozšířenou Windows variantu. Root-relative (`\shared\data`) i drive-relative (`C:shared\data`) vstupy se odmítnou, protože jejich cíl může záviset na aktuálním disku či pracovním adresáři cizího procesu, které nelze bezpečně odvodit z příkazové řádky. Obě cesty se poté normalizují pro porovnání; oddělovače kořene zůstávají zachované.

Nový scénář `20e` vychází z 20a. Po ukončení nové instance se při neúspěšném `GetOwner()` objeví cizí běžící gateway s jiným executable a `-data-dir "\shared\data"`. Rollback končí `RecoveryFailed`/`20` ještě před obnovou. Cizí proces zůstává běžet, původní se nespustí a test potvrdí beze změny obsah i ACL chráněných souborů. Scénář 20d pro koncový oddělovač i root-preservation kontrola zůstávají zachované.

Přímý výstup nové regrese a požadovaných kontrol:

```text
ISOLATED_SCENARIO=20d_other_gateway_trailing_separator_data_dir exit=20 state=RecoveryFailed running_ids=3004 data=protected guard=trailing_separator_normalized=yes foreign_process_preserved=yes original_not_restarted=yes target_content_acl_unchanged=yes root_path_preserved=yes result=PASS
ISOLATED_SCENARIO=20e_other_gateway_root_relative_data_dir exit=20 state=RecoveryFailed running_ids=3005 data=protected guard=root_relative_path_rejected=yes foreign_process_preserved=yes original_not_restarted=yes target_content_acl_unchanged=yes result=PASS
ISOLATED_SUBPROCESS_MATRIX=PASS scenarios=28 measured_exit_codes=yes actual_trap=yes actual_handler=yes actual_rollback=yes
TRANSACTION_SELFTEST_EXIT_CODE=0
PARSER=PASS file=runbook-transaction-selftest.ps1 errors=0
PARSER=PASS file=runbook-startup-selftest.ps1 errors=0
PARSER=PASS file=verify-managed-ptz-runtime.ps1 errors=0
POWERSHELL_PARSER_EXIT_CODE=0
RUNBOOK_STARTUP_SELFTEST=PASS pid=8101 state=empty initializer_gets=1 ready_poll=1
RUNBOOK_TCP_FAILURE_SELFTEST=PASS bearer_requests=0
RUNBOOK_HTTP_TIMEOUT_SELFTEST=PASS initializer_gets=1 retry_gets=0
STARTUP_SELFTEST_EXIT_CODE=0
GIT_DIFF_CHECK_EXIT_CODE=0
GATEWAY_PATH=C:\DEV\xiaomi-local-gateway\.test-env\builds\managed-ptz-finalization-20261109-f\gateway.exe
GATEWAY_SHA256=591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0
GATEWAY_SHA256_MATCH=PASS
GATEWAY_SHA256_EXIT_CODE=0
```

`git diff --check` vrátil kód 0 s upozorněními na LF/CRLF v pracovním stromu. Ověření zůstalo offline; gateway, Go zdroje a skutečné runtime prostředí se nezměnily.
