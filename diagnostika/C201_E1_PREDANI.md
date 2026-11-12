# Xiaomi C201 E1 — podmíněné nasazení a rollback

**Tento původní návrh je nahrazen dokumentem diagnostika/C201_E1_PREDANI_FINAL.md.** Starší příkazové bloky níže jsou neúplné a nesmějí se použít. Nový dokument je autoritativní předání. PID 38416 je historický.

Tento runbook je příprava k revizi, nikoli pokyn ke spuštění nyní. Všechna runtime data níže musí být zjištěna čerstvě v běžném Windows hostitelském kontextu. PID `38416` je historický a nesmí být použit jako cíl bez nového ověření.

## Aktuální doložený stav

- Vlastníkem nahlášená IP C201 je `192.168.2.124`; její dosažitelnost ani identita zařízení zatím nejsou nezávisle ověřeny.
- Poslední read-only kontrola neviděla PID `38416` ani gateway procesy pojmenované `xlg*`/`gateway`. CIM, TCP listener query i firewall `ActiveStore` vrátily odepřený přístup. Cílový proces, listenery, session a firewall jsou proto neověřené.
- Testovací gateway: `C:\DEV\xiaomi-local-gateway\.test-env\builds\managed-ptz-finalization-20261109-f\gateway.exe`; SHA-256 `591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0`.
- Žádný z následujících bloků nebyl spuštěn. Firewall, běžící gateway ani kamera nebyly změněny.

## 1. Čerstvá read-only identifikace cíle

Spustit později ve stejném Windows účtu jako gateway. Při chybě CIM/NetTCP, neznámém pracovním adresáři, neshodě launcher záznamu nebo více aktivních gateway skončit před odstávkou.

```powershell
$ErrorActionPreference = 'Stop'
$TargetPid = [int](Read-Host 'PID zjištěný právě teď; historický PID nepřebírat')
$wmi = Get-CimInstance Win32_Process -Filter "ProcessId=$TargetPid" -ErrorAction Stop
if (-not $wmi -or -not $wmi.ExecutablePath -or -not $wmi.CommandLine) { throw 'Cíl není úplně čitelný; stop.' }
$proc = Get-Process -Id $TargetPid -ErrorAction Stop
$startUtc = $proc.StartTime.ToUniversalTime()
$owner = Invoke-CimMethod -InputObject $wmi -MethodName GetOwner -ErrorAction Stop
if ($owner.ReturnValue -ne 0) { throw 'Vlastník procesu není ověřitelný; stop.' }
$ownerSid = ([Security.Principal.NTAccount]::new($owner.Domain,$owner.User)).Translate([Security.Principal.SecurityIdentifier]).Value
$currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
if ($ownerSid -ne $currentSid) { throw 'Jiný Windows/DPAPI účet; stop.' }
if ($wmi.CommandLine -notmatch '(?i)(?:^|\s)-listen\s+(?:"(?<listen>[^"]+)"|(?<listen>\S+))' -or $Matches.listen -notmatch '^127\.0\.0\.1:(?<port>\d+)$') { throw 'Neznámé API argumenty; stop.' }
$ApiListen = $Matches.listen
$ApiPort = [int]$Matches.port
if ($wmi.CommandLine -notmatch '(?i)(?:^|\s)-data-dir\s+(?:"(?<data>[^"]+)"|(?<data>\S+))') { throw 'Chybí data-dir; stop.' }
$DataDir = [IO.Path]::GetFullPath($Matches.data)
if (-not [IO.Path]::IsPathRooted($Matches.data)) { throw 'Relativní data-dir; stop.' }
$RunRoot = Split-Path -Parent $DataDir
$PrivateDir = Join-Path $RunRoot 'private'
$TargetExe = [IO.Path]::GetFullPath($wmi.ExecutablePath)
$handle = [Diagnostics.Process]::GetProcessById($TargetPid)
if ($handle.HasExited -or [IO.Path]::GetFullPath($handle.MainModule.FileName) -ne $TargetExe -or $handle.StartTime.ToUniversalTime() -ne $startUtc) { throw 'Proces se změnil; stop.' }
$apiListener = @(Get-NetTCPConnection -State Listen -LocalPort $ApiPort -ErrorAction Stop)
if ($apiListener.Count -ne 1 -or $apiListener[0].OwningProcess -ne $TargetPid -or $apiListener[0].LocalAddress -ne '127.0.0.1') { throw 'API listener nepatří cíli; stop.' }
$launchFile = Join-Path $RunRoot 'firewall-target.json'
$tokenFile = Join-Path $PrivateDir 'api-token.txt'
$logFile = Join-Path $PrivateDir 'gateway.stderr.log'
if (-not (Test-Path -LiteralPath $launchFile) -or -not (Test-Path -LiteralPath $tokenFile) -or -not (Test-Path -LiteralPath $logFile)) { throw 'Chybí privátní soubory instance; stop.' }
$launch = Get-Content -Raw -LiteralPath $launchFile | ConvertFrom-Json
if ($launch.gateway_pid -ne $TargetPid -or $launch.api_port -ne $ApiPort -or [IO.Path]::GetFullPath($launch.gateway_executable) -ne $TargetExe -or $launch.camera_address -ne '192.168.2.124') { throw 'Startovní manifest nesouhlasí s procesem/IP; stop.' }
$RtspPort = [int]$launch.rtsp_port
$RtspAdvertiseHost = [string]$launch.local_address
# Nezobrazovat raw command line, token, DID ani RTSP URL.
[pscustomobject]@{ pid=$TargetPid; owner_sid=$ownerSid; executable=$TargetExe; start_utc=$startUtc.ToString('o'); api_listen=$ApiListen; data_dir=$DataDir; working_directory=$RunRoot; rtsp_port=$RtspPort; rtsp_advertise_host=$RtspAdvertiseHost } | Format-List
```

## 2. Read-only firewall gate před odstávkou

`ActiveStore` je pouze dotaz. Protokol musí být TCP/Any; pravidla s portem `49321`, rozsahem obsahujícím port, nebo `Any` se musí posoudit spolu s `Program`, profilem, lokální adresou a skutečnou klientskou adresou. Jakékoli nečitelné nebo nejednoznačné pravidlo znamená `UNVERIFIED` a zákaz pokračovat.

```powershell
$ErrorActionPreference = 'Stop'
$NewExe = 'C:\DEV\xiaomi-local-gateway\.test-env\builds\managed-ptz-finalization-20261109-f\gateway.exe'
Get-NetConnectionProfile -ErrorAction Stop | Select-Object InterfaceAlias,NetworkCategory,IPv4Connectivity
Get-NetFirewallProfile -PolicyStore ActiveStore -ErrorAction Stop | Select-Object Name,Enabled,DefaultInboundAction,AllowLocalFirewallRules
$allRules = Get-NetFirewallRule -PolicyStore ActiveStore -Direction Inbound -Enabled True -ErrorAction Stop
foreach ($rule in $allRules) {
    $port = $rule | Get-NetFirewallPortFilter -ErrorAction Stop
    $app = $rule | Get-NetFirewallApplicationFilter -ErrorAction Stop
    $addr = $rule | Get-NetFirewallAddressFilter -ErrorAction Stop
    if ($port.Protocol -in @('TCP','6','Any') -and ($port.LocalPort -eq 'Any' -or [string]$port.LocalPort -match '49321|\d+-\d+')) {
        [pscustomobject]@{ Name=$rule.Name; Action=$rule.Action; Profile=$rule.Profile; Program=$app.Program; Protocol=$port.Protocol; LocalPort=$port.LocalPort; LocalAddress=$addr.LocalAddress; RemoteAddress=$addr.RemoteAddress }
    }
}
# Před odstávkou musí být ručně potvrzeno, že efektivní ActiveStore dovoluje
# TCP RTSP 49321 pro $NewExe a skutečného klienta a že neexistuje odpovídající Block.
# Při odepřeném dotazu nebo nejasném rozsahu: žádný stop/start ani změna firewallu.
```

## 3. Hash a zabezpečená záloha

Hash kontrolovat před odstávkou. Záloha obsahuje soukromé údaje; umístit ji pod privátní adresář se zděděným ACL. Binárku původní instance nepřepisovat.

```powershell
$ErrorActionPreference = 'Stop'
$NewExe = 'C:\DEV\xiaomi-local-gateway\.test-env\builds\managed-ptz-finalization-20261109-f\gateway.exe'
$ExpectedHash = '591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0'
$actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $NewExe -ErrorAction Stop).Hash
if ($actualHash -ne $ExpectedHash) { throw 'Gateway SHA-256 mismatch; stop.' }
$stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ')
$BackupRoot = Join-Path $PrivateDir "e1-backup-$stamp"
New-Item -ItemType Directory -Path $BackupRoot -ErrorAction Stop | Out-Null
Set-Acl -LiteralPath $BackupRoot -AclObject (Get-Acl -LiteralPath $PrivateDir)
$stateFile = Join-Path $DataDir 'managed-state.json'
foreach ($pair in @(
    @($TargetExe,(Join-Path $BackupRoot 'original-gateway.exe')),
    @($tokenFile,(Join-Path $BackupRoot 'api-token.txt')),
    @((Join-Path $RunRoot 'firewall-target.json'),(Join-Path $BackupRoot 'firewall-target.json')),
    @($logFile,(Join-Path $BackupRoot 'gateway.stderr.log')),
    @((Join-Path $PrivateDir 'gateway.stdout.log'),(Join-Path $BackupRoot 'gateway.stdout.log')),
    @((Join-Path $env:APPDATA 'XiaomiViewer/config.json'),(Join-Path $BackupRoot 'XiaomiViewer-config.dpapi.json'))
)) { Copy-Item -LiteralPath $pair[0] -Destination $pair[1] -ErrorAction Stop }
# Po čistém zastavení původního PID teprve kopírovat managed-state.json a jeho ACL:
# Copy-Item -LiteralPath $stateFile -Destination (Join-Path $BackupRoot 'managed-state.json')
# (Get-Acl -LiteralPath $stateFile).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::All) |
#   Set-Content -LiteralPath (Join-Path $BackupRoot 'managed-state.acl.sddl') -NoNewline
```

Pokud kopie stavu nebo ACL selže po zastavení, nespouštět novou binárku. Obnovit původní log, zachovat původní `managed-state.json` na místě a spustit původní executable přes stejnou funkci `Start-C201Gateway` níže. Jestli původní data nejsou čitelná, zůstat zastavený a eskalovat; stav neodhadovat.

## 4. Zastavení a spuštění vedle sebe

Před `Kill()` znovu načíst CIM a porovnat PID, executable, argumenty, vlastníka a čas startu s výše uloženým snapshotem. Zastavovat přes již otevřený `$handle`, ne přes samotný PID:

```powershell
$fresh = Get-CimInstance Win32_Process -Filter "ProcessId=$TargetPid" -ErrorAction Stop
$freshProc = Get-Process -Id $TargetPid -ErrorAction Stop
$freshOwner = Invoke-CimMethod -InputObject $fresh -MethodName GetOwner -ErrorAction Stop
$freshSid = ([Security.Principal.NTAccount]::new($freshOwner.Domain,$freshOwner.User)).Translate([Security.Principal.SecurityIdentifier]).Value
if ($handle.HasExited -or $freshSid -ne $ownerSid -or [IO.Path]::GetFullPath($fresh.ExecutablePath) -ne $TargetExe -or $freshProc.StartTime.ToUniversalTime() -ne $startUtc -or $fresh.CommandLine -ne $wmi.CommandLine) { throw 'Identita se změnila; nic nezastavovat.' }
$handle.Kill()
if (-not $handle.WaitForExit(10000)) { throw 'Původní proces se neukončil; nespouštět náhradu.' }
```

Novou instanci spustit pod stejným SID/DPAPI účtem, v `$RunRoot`, se stejnými `$ApiListen` a `$DataDir`. Zachovat existující API tokenový soubor a DPAPI profil; `XLG_RTSP_ADVERTISE_HOST` odvodit z ověřeného `firewall-target.json.local_address` a `XLG_RTSP_LISTEN` z `0.0.0.0:$RtspPort`, jak je nastavuje pilotní launcher. Proměnné se nastaví jen v rodičovském PowerShellu těsně před `Start-Process`, hodnoty se nevypisují a prostředí se ihned obnoví. Nepoužívat stávající launcher beze změny, protože vybírá jiné porty a `data-dir`.

```powershell
# Po ověření profilu DPAPI a tokenového souboru v paměti, bez jejich výpisu:
$env:XLG_API_TOKEN = $ApiTokenFromPrivateFile
$env:XLG_XIAOMI_USER_ID = $UserIdFromCurrentUserDpapiProfile
$env:XLG_XIAOMI_REGION = $RegionFromCurrentUserDpapiProfile
$env:XLG_XIAOMI_TOKEN = $XiaomiTokenFromCurrentUserDpapiProfile
$env:XLG_RTSP_ADVERTISE_HOST = $RtspAdvertiseHost
$env:XLG_RTSP_LISTEN = "0.0.0.0:$RtspPort"
$argLine = "-managed -listen $ApiListen -data-dir `"$DataDir`""
$newProcess = Start-Process -FilePath $NewExe -ArgumentList $argLine -WorkingDirectory $RunRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $PrivateDir 'gateway.stdout.log') -RedirectStandardError $logFile
# Ihned vyčistit/obnovit procesové proměnné v finally bloku; ověřit nový PID, cestu,
# argumenty, vlastníka, čas startu a listener před jakýmkoli API požadavkem.
```

Následně se stejným privátním tokenovým souborem a až po ověření listeneru udělat GET `/v2/info`. GET `/v2/cameras` může vytvořit RTSP cestu/session: před prvním GETem musí být ověřeno, že žádná další gateway nemá aktivní spojení/session k C201; po GETu musí být právě jedna session a jedna C201. Pak kontinuální RTSP/TCP odběr, nejméně osm souvislých stabilních snímků 848×480. Pokud inventory cache není čerstvá, log či socket není jednoznačný nebo se otevře další session, žádný PTZ POST.

## 5. Rollback

Nejdřív ověř nový PID přes čerstvý CIM snapshot (cesta k nové binárce, přesné argumenty/data-dir, vlastník a start UTC). Pokud PID chybí, zastavit lze jen jednu jednoznačnou shodu všech těchto polí; při nule nebo více shodách nikoho nezastavovat. Po zastavení musí read-only enumerace všech dostupných gateway procesů potvrdit, že žádný nepoužívá `$DataDir`; nečitelný command line, relativní `data-dir` nebo neúplný CIM výpis znamená stop bez obnovy.

```powershell
# Teprve po prokázaném ukončení všech procesů používajících $DataDir:
Copy-Item -LiteralPath (Join-Path $BackupRoot 'managed-state.json') -Destination $stateFile -Force -ErrorAction Stop
$savedSddl = Get-Content -Raw -LiteralPath (Join-Path $BackupRoot 'managed-state.acl.sddl')
$stateAcl = [Security.AccessControl.FileSecurity]::new()
$stateAcl.SetSecurityDescriptorSddlForm($savedSddl,[Security.AccessControl.AccessControlSections]::All)
Set-Acl -LiteralPath $stateFile -AclObject $stateAcl
$roundTripSddl = (Get-Acl -LiteralPath $stateFile).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::All)
if ($roundTripSddl -ne $savedSddl) { throw 'ACL state restore mismatch; do not start a gateway.' }
Copy-Item -LiteralPath (Join-Path $BackupRoot 'gateway.stderr.log') -Destination $logFile -Force -ErrorAction Stop
# Start-C201Gateway -Executable $TargetExe -Listen $ApiListen -DataDir $DataDir -WorkingDirectory $RunRoot
```

Start původní executable stejným DPAPI/token/environment blokem a stejnými argumenty, working directory, data-dir, porty i RTSP proměnnými. Po startu ověř owner, listener, API `/v2/info`, inventář a stream. Restart ukončí původní CS2 session; nová session může vyžadovat Xiaomi Cloud. Žádná část rollbacku nesmí sama opakovat PTZ.

## 6. Budoucí jediný řízený PTZ krok

Po samostatném schválení a ověření aktuální fáze 0: jeden gateway PID, právě jedna CS2/TCP session k C201, žádná další gateway session, bezpečný inventory GET, RTSP socket svázaný s touto instancí a alespoň osm stabilních souvislých snímků 848×480 s drift ≤2 px. Helper posílá maximálně jeden `left`; při timeoutu POST neopakuje. Report oddělí HTTP `write_status`, `0x113`/`no_reply`/unverified, posun obrazu a fyzické otočení. `reply_received` není důkaz fyzického pohybu.
