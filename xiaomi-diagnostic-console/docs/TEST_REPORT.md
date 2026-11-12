# Výsledky ověření balíčku v0.1

**Datum:** 2026-11-11. Testovací platforma: izolované Linux prostředí s Go `1.23.2`, FFmpeg nainstalovaný, bez síťového přístupu ke skutečné C201. Windows x64 verze byla cross-compiled pomocí `CGO_ENABLED=0`.

| Test | Výsledek |
|---|---|
| `go test ./... -count=1` | **PASS** |
| `go test -race ./... -count=1` | **PASS** (Linux; není to Windows race run) |
| `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ...` | **PASS** |
| `node --check internal/console/static/app.js` | **PASS** |
| HTTP GET `/` a `/api/status` na běžícím lokálním mock serveru | **HTTP 200** |
| HTTP POST `/api/video/start` a `/api/video/stop` (mock) | **HTTP 200**, snímky a FPS přibývají |
| HTTP POST `/api/ptz/left` po explicitním potvrzení (mock) | **HTTP 200**, `outcome=simulated`, `write_status=simulated_only` |
| Neoprávněný POST / cizí Origin / cizí Host | **Odmítnuto (HTTP 403)** |
| Read-only GET status bez inventory GET | **Ověřeno unit testem** |
| Cílené přidělení MJPEG workeru více klientům | **Ověřeno unit testem** |
| Odmítnutí cest mimo loopback a nepovoleného RTSP hostu | **Ověřeno unit testem** |
| Odmítnutí symlink nahrávky | **Ověřeno unit testem** |
| V původním běhu: spuštění Windows EXE, živé C201 video, živý PTZ a SD karta | **NEOVĚŘENO** |

Původní cross-compiled artefakt `bin/diagconsole.exe` (PE32+ `windows/amd64`), SHA-256:

```
37df1bc5ae16b3eff70e6f488440449fc64778c630db7995c86dcb4b7b665ed4
```

**Tento hash patří mini klientovi, nikoliv gateway!** SHA gateway z E1 je odlišné: `591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0`.

Záměrně nebylo spuštěno nic, co by se připojilo ke skutečné C201 nebo zasáhlo do gateway. Skutečný PowerShell runbook E1 a jeho binárka existují pouze na uživatelském Windows stroji. Uživatel musí pro živé PTZ doložit čerstvý runtime preflight.

## Kontrolní běh ve Windows workspace, 2026-11-11

| Test | Výsledek |
|---|---|
| Windows Go `1.27.3`, `go test ./... -count=1` | **PASS** |
| `node --check internal/console/static/app.js` | **PASS** |
| Windows amd64 build, CGO vypnuto | **PASS** |
| Přímé spuštění předchozího buildu s `config.mock.json` | **PASS**, proces ohlásil mock režim; tato hláška nepotvrzuje HTTP dostupnost |
| Mock video, inventory a PTZ přes Go handler testy | **PASS**, bez fyzické kamery a bez síťového mock serveru |
| HTTP smoke test a vizuální stránka z tohoto sandboxu | **BLOKOVÁNO**, loopback socket odmítl PowerShell a browser skončil `ERR_CONNECTION_TIMED_OUT` |

Hash buildu před opravou zachování mock inventáře:

```
758F9BA90E3B139A98BD8CCEE0E787C13DB58F647EE9E918E30AE8706098FDE8
```

`Build-Windows.ps1` nyní bere Go z `PATH`, `GO_EXE` nebo parametru `-GoPath`; přímé spuštění `.ps1` v tomto workspace blokuje lokální PowerShell execution policy. Stejné testovací a build příkazy byly proto spuštěny přímo s lokální instalací Go. Demo bylo spuštěno přímo z nově sestaveného EXE. Live gateway, kamera, PTZ a SD karta zůstaly nedotčeny.

## Poznámka k vizuální verifikaci

V původním izolovaném běhu byly HTTP endpoints ověřeny standardním klientem. Ve Windows workspace loopback PowerShellu odmítl a browser vrátil `ERR_CONNECTION_TIMED_OUT`, proto zde nelze potvrdit dostupnost UI z běžícího EXE ani pořídit screenshot. Embedded assets a ovládací cesty jsou místo toho ověřené níže přes Go HTTP handler bez otevření síťového socketu.

## Doplňující handler ověření po opravě mock statusu, 2026-11-11

| Test přes Go `httptest.ResponseRecorder` | Výsledek |
|---|---|
| `GET /`, `/app.css`, `/app.js` | **PASS**, HTTP 200, očekávaný MIME typ a obsah stránky každého assetu |
| Mock `GET /api/status` před inventory refresh | **PASS**, inventář zůstává nenačtený |
| Mock `POST /api/inventory/refresh` a následný status | **PASS**, jedna C201; status inventář zachová |
| Mock video start/stop | **PASS**, JPEG snímky vznikají a jsou dostupné přes preview worker |
| Mock PTZ po potvrzení | **PASS**, `outcome=simulated`, `http_write_status=simulated_only`, pohyb `not_measured` |
| `go test ./... -count=1` a kontrola JS | **PASS** (Windows workspace, Go 1.27.3) |
| Windows amd64 build s CGO vypnuto | **PASS** |

Regresní test odhalil, že mock `/api/status` po refreshi inventáře nevracel uložený souhrn; opravil jsem jej a test nyní potvrzuje, že se v UI statusu objeví jedna C201.

Windows EXE před opravou vrstvy skrytého náhledu, SHA-256:

```
43122A40806B4DE751391B16C4A08AA66E859585269FAE48449DCBB7286F9B6B
```

HTTP požadavky na samostatný běžící EXE zůstávají **BLOKOVANÉ** omezením loopbacku tohoto sandboxu. Handler testy neprokazují dosažitelnost procesu přes socket; živá gateway ani kamera nebyly použity.

## Oprava viditelnosti náhledu a finální build, 2026-11-11

Do `app.css` jsem přidal `[hidden]{display:none!important}`, aby výchozí `display:flex` placeholderu nemohl přebít HTML atribut `hidden`. Handler test `TestEmbeddedUIAssetsServedThroughHandler` ověřuje HTTP 200 a obsah skutečného embedded HTML/CSS/JS; pro CSS kontroluje přímo pravidlo skrytí. `TestMockInventoryRefreshThroughHandler`, `TestVideoMockOneWorker` a `TestMJPEGUsesExistingWorker` ověřují mock inventář, JPEG snímky a preview stream přes handlery.

| Ověření | Výsledek |
|---|---|
| `go test ./... -count=1` | **PASS** (Windows workspace, Go 1.27.3) |
| `node --check internal/console/static/app.js` | **PASS** |
| `node scripts/Test-Preview-UI.js` | **PASS**, actual HTML/CSS/JS, test DOM and mock fetch/MJPEG |
| Windows amd64 build s CGO vypnuto | **PASS** |
| Headless Chrome / Edge UI přechod start/stop | **BLOKOVÁNO před vykreslením**; Chrome skončil `-2147483645`, Edge `-1073741819` při chybě `CreateAppContainerProfile` (`0x80070057`) |
| HTTP požadavky na běžící EXE | **BLOKOVÁNO** omezením loopbacku |

`Test-Preview-UI.js` ověřil přechod z vypnutého náhledu na spuštěný a zpět: při startu `placeholder.hidden=true`, `image.hidden=false`, testovací mock snímek má rozměry 848 px a UI ukazuje 4 snímky; po stopu se placeholder zobrazí, obrázek se skryje a jeho zdroj se vymaže. Jde o test DOM stavů a mock odpovědi, ne o vykreslení pixelů plnohodnotným browser enginem.

Aktuální Windows x64 `bin/diagconsole.exe`, SHA-256:

```
900896302E0F987945B7472095591519509FBDD1ACAECDBE8D5F982E5CDA985C
```

Headless test placeholderu a obrázku se tedy nepodařilo dokončit v prohlížeči. Embedded assety a handlerové mock odpovědi prošly bez síťového socketu; běh browseru ani HTTP dostupnost aktuálního EXE tím nejsou prokázané. Živá gateway a kamera nebyly použity.

## Opakovaná kontrola dostupných prostředí, 2026-11-11

- Node DOM test byl znovu spuštěn: **PASS**. Start hlásí `placeholderHidden=true`, `imageHidden=false`, `imageLoaded=true`, 4 snímky; stop obrátí viditelnost a vymaže zdroj obrázku.
- Samostatný Windows loopback self-test skončil chybou oprávnění socketu. Proto nebylo možné připojit se k aktuálnímu EXE a `scripts/Test-Mock.ps1` nebyl spuštěn.
- WSL launcher je přítomný, ale `wsl --status` i `wsl --list --quiet` vrací `Wsl/E_ACCESSDENIED`; dostupnost linuxové distribuce tak nešlo ověřit ani spustit. Docker runtime zde není dostupný.
- Headless Windows Chrome/Edge zůstávají blokované chybou AppContaineru uvedenou výše. Nebylo nalezeno jiné dostupné prostředí s browser enginem.

Aktuální EXE zůstává `900896302E0F987945B7472095591519509FBDD1ACAECDBE8D5F982E5CDA985C`; jeho HTTP dostupnost nebyla v tomto prostředí ověřena.

## Read-only kontrola gateway pro testovací kameru, 2026-11-11

- `Get-Process` neukázal proces gateway ani diagnostické konzole a `netstat -ano` neukázal TCP listenery. `Get-NetTCPConnection` odmítl přístup k CIM, takže aktuální listener nelze potvrdit.
- `config.live.json` v adresáři konzole chybí; `config.live.example.json` obsahuje zástupné cesty `host-run-REPLACE`.
- Proto nebyla vytvořena živá konfigurace, spuštěn `Start-Test-Camera.cmd` ani odeslán HTTP požadavek. `scripts/Test-Mock.ps1` vyžaduje dostupnou konzoli na loopbacku, který je zde blokovaný.
- Tokenový ani RTSP soubor jsem nečetl. Žádná gateway nebyla spuštěna, zastavena, nasazena ani restartována.

## Spouštěč testovací kamery bez ruční šablony, 2026-11-11

`Start-Test-Camera.cmd` bez argumentů nyní hledá aktivní managed běh v `.test-env/c201-pilot/win-gateway/host-run-*`. Přijme ho pouze při shodě uloženého PID a executable, existenci tokenového souboru, FFmpegu a HTTP `GET /healthz`; poté vytvoří ignorovaný `config.live.json` s dynamickým API portem, RTSP discovery přes ruční inventory refresh a `ptz.enabled=false`. Token ani RTSP URL nevypisuje. Gateway nespouští ani nerestartuje. Předání vlastní připravené konfigurace zůstává možné jako argument.

Při původní kontrole nebyl žádný PID z 59 runtime záznamů spárován s běžícím procesem; poslední existující tokenový soubor tehdy byl z 2026-11-07 a RTSP URL soubor chyběl. Proto jsem spouštěč proti živé kameře nespustil a `config.live.json` nevytvořil. Inline PowerShell část spouštěče prošla kontrolou syntaxe; skutečný no-argument běh vyžaduje, aby testovací gateway už běžela z odpovídajícího `host-run-*`.

## Oprava cesty spouštěče a no-argument kontrola, 2026-11-11

- `Start-Test-Camera.cmd` nejprve přejde do vlastního adresáře přes `pushd "%~dp0"`; z něj nyní skládá runtime kořen jako `..\.test-env\c201-pilot\win-gateway`. Ověření z pracovního adresáře `%TEMP%` dalo absolutní cestu `C:\DEV\xiaomi-local-gateway\.test-env\c201-pilot\win-gateway`, shodnou s projektovým runtime adresářem.
- Kontrola 62 `host-run-*` adresářů nenašla žádný běžící proces, jehož PID a cesta k executable odpovídají runtime manifestu.
- Skutečné spuštění `Start-Test-Camera.cmd` bez argumentů z `%TEMP%` proto skončilo očekávaným kódem 4 a zprávou „No verified running test gateway was found“. Spouštěč gateway nespustil ani nezměnil; nevytvořil `config.live.json`.
- Bez aktivní ověřené gateway nebylo možné ověřit autentizovaný HTTP stav, ručně vyžádat inventář C201 ani získat JPEG z živého náhledu. Žádný požadavek na inventář nebo kamerovou session nebyl odeslán. Po spuštění testovací gateway lze `.cmd` znovu spustit bez argumentů; PTZ v automaticky vytvořené konfiguraci zůstává vypnuté.
- Po nahrazení chybového výpisu stručnou hláškou proběhlo no-argument spuštění znovu: **PASS pro bezpečné odmítnutí při nepřítomné gateway**, exit code 4; inline PowerShell znovu prošel parserem. `config.live.json` zůstal nevytvořen.

## Pokus o spuštění živé testovací gateway, 2026-11-11

- Startér `.test-env/c201-pilot/win-gateway/start-host-gateway.ps1` byl spuštěn bez `-ExposeApi` a bez `-AddScopedFirewall`. Výchozí parametr pro FFmpeg v prvním pokusu selhal při vyhodnocení prázdného `$PSScriptRoot`; opakovaný pokus s explicitní cestou k existujícímu `ffmpeg.exe` pokračoval.
- Gateway proces se při startu ukončil. Z bezpečně filtrovaného diagnostického logu: `xiaomi_backend event=account_restore result=error reason=dns`. PID již nemá běžící proces; metadata běhu uvádí `api_exposed=false`. Firewall nebyl měněn.
- `Start-Test-Camera.cmd` bez argumentů byl poté spuštěn z `%TEMP%`; správně odmítl neaktivní běh s kódem 4. `config.live.json` nevznikl a konzole nebyla spuštěna.
- Kvůli chybě DNS nebyl dostupný běžící gateway endpoint. Autentizovaný stav, inventář C201 ani živý JPEG proto nebyly ověřeny; žádný další požadavek na inventář nebo kamerovou session nebyl odeslán. Záznam neobsahuje token ani RTSP URL.

## Úspěšný headless živý test C201, 2026-11-11

### DNS a start gateway

- Předchozí omezený běh zaznamenal pouze `reason=dns`, nikoli jméno z `net.DNSError`. Zdroj `LoginWithToken` ukazuje, že obnova účtu začíná HTTPS požadavkem na `account.xiaomi.com`. V omezeném procesu selhaly Windows DNS lookupy pro `account.xiaomi.com` i EU shard `de.api.io.mi.com` výjimkou `SocketException`; hodnoty ani síťové identifikátory nebyly do reportu ukládány.
- V povoleném Windows kontextu stejného uživatele DNS vrátilo A záznam pro `account.xiaomi.com` a dva A záznamy pro `de.api.io.mi.com`. Gateway spuštěná v tomto kontextu potvrdila `account_restore result=ok`; tím prošla skutečná obnova účtu i následující živý inventory požadavek. Zdrojové prostředí nemělo nastavené proxy proměnné.
- Existující `start-host-gateway.ps1` byl spuštěn s explicitním `-FfmpegPath`, bez `-ExposeApi` a bez `-AddScopedFirewall`. Startér ověřil autentizaci, inventář čtyř kamer a jednu `xiaomi.camera.c201`; stream endpoint prošel interní validací. Metadata potvrzují `api_exposed=false`. Firewall nebyl změněn.

### Konzole a obraz

- `Start-Test-Camera.cmd` byl spuštěn bez argumentů z `%TEMP%`. Ověřil aktivní managed běh, bez ruční konfigurace vytvořil ignorovaný `config.live.json` a spustil aktuální Windows x64 konzoli.
- Headless HTTP: HTML `/` vrátilo 200; `/api/status` hlásil `mock=false`, gateway HTTP 200, autentizované `info_status=200` a `ptz.enabled=false`.
- Explicitní POST `/api/inventory/refresh` vrátil 200, načetl čtyři kamery, jednu C201 a jeden povolený stream endpoint.
- POST `/api/video/start` uspěl. Status potvrdil běžící náhled a JPEG snímky; při zastavení bylo napočítáno 229 snímků. FFmpeg přes HTTP `/api/video/mjpeg` úspěšně dekódoval jeden JPEG 848×480 do null výstupu. Obraz nebyl ukládán. POST `/api/video/stop` uspěl a vrátil `running=false`.
- PTZ nebylo voláno; vygenerovaná konfigurace má `ptz.enabled=false`. Po ověření byl zastaven pouze skrytý konzolový proces a uvolněn port 18880; živá testovací gateway zůstala běžet a její loopback health vrátil 200, aby uživatel mohl nyní spustit svůj `.cmd`. API zůstává na `127.0.0.1`; firewall se neměnil.
- Test proběhl headless přes HTTP a FFmpeg, bez interakce s plochou. Nešlo o vykreslení v plnohodnotném browser enginu. Žádný token ani RTSP URL tento protokol neobsahuje.

## Jednopříkazový start gateway a konzole, 2026-11-11

- No-argument větev `Start-Test-Camera.cmd` nyní volá `scripts/Prepare-Test-Camera.ps1`. Helper znovu použije pouze běžící managed gateway se shodným PID/executable, health 200 a `api_exposed=false`; pokud taková gateway chybí, zavolá projektový startér s explicitním `ffmpeg.exe`, bez `-ExposeApi` a bez `-AddScopedFirewall`, a až potom vytvoří živou konfiguraci. Helper prošel PowerShell parserem.
- Obě větve jsou ověřené z `%TEMP%`: při živé gateway výstup označil `LOCAL_GATEWAY=existing verified process`; po ukončení tohoto managed běhu jediný no-argument `.cmd` vytvořil nový běh (`LOCAL_GATEWAY=started and verified`) a spustil konzoli. `config.live.json` se vytvořil automaticky, bez ručního JSON. Gateway hlásila `account_restore ok`, `inventory ok` pro 4 kamery, jednu C201, úspěšné vyjednání/otevření session, první H.265 media a připravený RTSP relay.
- V nové větvi `/api/status` hlásil live režim, gateway HTTP 200, autentizované `info_status=200` a PTZ vypnuté. Explicitní POST `/api/inventory/refresh` vrátil 200, 4 kamery, jednu C201 a jeden povolený stream endpoint. POST `/api/video/start` uspěl a status začal vracet snímky.
- Přímý FFmpeg MPJPEG demux nad nekonečným HTTP multipart streamem v tomto běhu skončil `Invalid data found`. Samostatný headless odběr z téhož `/api/video/mjpeg` přečetl hlavičku multipart části a její `Content-Length`; první část obsahovala 9 210 JPEG bajtů s platnými SOI/EOI markery. FFmpeg `image2pipe` tento skutečný JPEG dekódoval jako 848×480. Obraz nebyl ukládán.
- POST `/api/video/stop` uspěl a status potvrdil `running=false`. Ověřovací konzole byla ukončena a port 18880 uvolněn; nová managed gateway zůstala běžet pro okamžité spuštění uživatelem. Metadata uvádí `api_exposed=false`; API je pouze na loopbacku, firewall se nezměnil, PTZ zůstalo vypnuté.
- Ověření proběhlo bez plochy a browseru, přes konzolový HTTP API a FFmpeg. Vykreslení HTML v plnohodnotném browser enginu tímto testem potvrzené není. V protokolu není token ani RTSP URL.

## PTZ C201: připojení konzole a jeden živý krok, 2026-11-11

- Uživatel potvrdil volný prostor kamery, vypnuté automatické sledování a funkční PTZ v Xiaomi Home. Dříve spuštěná testovací gateway odpovídala na bezpečný GET nové PTZ route 404. Ze současných zdrojů vznikl samostatný Windows build, který při izolovaném startu bez kamerového provideru vrátil na tentýž GET 405. Starou testovací gateway nahradil jediný nový managed běh s API na loopbacku.
- `Start-Test-Camera.cmd` nyní při ověření přesné gateway, PTZ helperu a runtime verifieru podle SHA-256 a dostupné route vytvoří aktuální PTZ konfiguraci automaticky; pro starší gateway zůstává PTZ vypnuté. Před každým krokem zůstává nutné potvrzení v konzoli. Runtime verifier nyní čte živý log se sdílením zápisu; read-only preflight potvrdil jeden CS2 TCP spoj a vlastníka API listeneru.
- První dva pokusy přes konzoli skončily `write_status=not_sent`; jeden se zastavil na příliš krátké baseline. Helper nyní při čtení vybírá nejnovější dostupný snímek, zachovává původní čas snímku a vzorkuje v intervalu 500 ms. Ověřování stability a minimálního třísekundového okna se nezkrátilo. Konzole zobrazuje bezpečně filtrovanou fázi blokace a rozlišuje skutečný výsledek obrazového porovnání.
- Třetí pokus přes konzoli odeslal právě jeden krok vlevo; HTTP výsledek `write_status=sent`, `reply_0x113=received`, `image_motion=inconclusive`. Allowlistovaný gateway log: `cmd_sent_id=0x112`, `payload={"operation":1}`, `write_status=write_ok`, `reply_status=reply_received`, `reply_cmd_id=0x113`, latence 136 ms. Další příkaz se neopakoval. Fyzický pohyb zůstává bez potvrzení pozorovatelem; obrazový výsledek jej nedokazuje.
- Konzole Go `go test ./... -count=1` prošly; cílené testy PTZ gateway handlerů, `internal/xiaomi/ptzprobe` a PTZ helperu včetně testů fronty a zákazu duplicitního zápisu prošly. Širší běh gateway testů měl dvě nesouvisející chyby Windows oprávnění pro managed state; širší suite helperu měla jednu nesouvisející chybu `TestRunbookOrdersPostStartGateBeforeInitializerAndReadiness`.
- Windows x64 SHA-256: gateway `53C5043226E3D69D22B93CF2FEB0B45E91876529AA72D35F017DB74B7E04A1EE`; konzole `FC2582943F609A1D3262E46AF4FACE3B236E8FDFBF2AC4F23CCCA82B0A52F18A`; PTZ helper `1E2B1B56BC72BBEB2BA3B6A83AE16E2D418371D40EECEABE0DDA7971E8E529A6`; runtime verifier `C159DC6FCB0DAC896CE4F4EE9BC446C4BA36367B663A98CA3BECE9D8D408CB25`. Ověřeno přes HTTP v lokálním Windows kontextu; plnohodnotný browser engine se netestoval. Firewall se neměnil; token ani RTSP URL nejsou v tomto reportu.

## PTZ follow-up: stabilita obrazu a většinový konsenzus, 2026-11-11

- Uživatel potvrdil statickou scénu. Read-only měření s původním limitem konsenzu 60 % našlo 12 konzistentních bodů z 23 (52,2 %), drift 0,00 px; baseline proto skončila jako nestabilní a žádný příkaz PTZ nebyl odeslán.
- Brána měření nyní vyžaduje přísnou většinu shod (>50 %), nejméně 6 bodů, stále vyžaduje 8 snímků v okně 3–5 s, maximální drift 2 px a shodu čerstvého snímku před zápisem. Tím se odstranilo odmítnutí statické scény při 12/23 shodách, aniž se povolila menšina.
- Nový read-only živý běh se stejným algoritmem potvrdil `PTZ_BASELINE status=stable frames=8 tracked_points=12 match_vectors=23 consensus_pct=52.2 drift_px=0.00 span_ms=3481`, `PTZ_PREWRITE status=ready` a následně `MANAGED_HTTP status=blocked stage=baseline_only request_count=0`. Snímek měl 848 × 480; kamera zůstala bez nového pohybového příkazu. Pozice hlavy kamery přes API nebyla dostupná.
- Cílené testy `internal/xiaomi/ptzprobe`, testy managed HTTP helperu pro aktuální PTZ session a frontu snímků i `xiaomi-diagnostic-console` `go test ./... -count=1` prošly. PowerShell parser a shoda SHA pinu helperu prošly také.
- Produkční helper pro Windows x64 byl sestaven z opraveného algoritmu, SHA-256 `88474A1606CD63974A268B8C4782DA90DEAD00D7C277E17890237747AA918052`. `Start-Test-Camera.cmd` jej načetl a lokální konzole na `127.0.0.1:18880` odpovídá. Další živý levý krok zatím nebyl s novým helperem odeslán; fyzický pohyb z dřívějšího potvrzeného zápisu zůstává nepozorovaný.

## PTZ C201: jeden živý krok a výsledek snímků, 2026-11-11

- Před krokem konzole odpovídala přes loopback, PTZ byla povolená a nečinná; explicitní refresh vrátil právě jednu C201. Byla použita standardní potvrzovací cesta konzole s `phase0_confirmed=true` a `ONE-LEFT-APPROVED`.
- Odeslán byl právě jeden POST. V gateway logu přibyl přesně jeden `PTZ_DIAG`: `cmd_sent_id=0x112`, `write_status=write_ok`, `reply_status=reply_received`, `reply_cmd_id=0x113`, latence 230 ms, žádná opožděná odpověď. Konzole vrátila `http_write_status=sent`, `reply_0x113=received`. Automatický retry nebyl proveden.
- Sběr snímků dokončil všechna okna: první obraz 848 × 480, baseline 8 snímků / 3447 ms (stabilní), čerstvý snímek před zápisem `ready`, přechod 20 snímků / 5 s a klidové okno 8 snímků / 3484 ms (stabilní). Dekodér zaznamenal 284 snímků. Sběr tedy neselhal.
- Porovnání přechodu vrátilo `no_consistent_shift`, 12 sledovaných bodů, medián posunu 0,00 × 0,00 px a 0 konzistentních snímků. Klidové porovnání vůči baseline bylo `inconclusive`; původní diagnostika zobrazila sentinel `9223372036854775807`, protože žádný snímek neprošel filtrem shody. Zjištěné selhání je v porovnání obrazu, nikoli v zápisu ani v načtení snímků. Odpověď 0x113 sama o sobě fyzický pohyb nepotvrzuje. Žádný další motorický příkaz nebyl odeslán.
- Následně byla diagnostika rozšířena o počty kandidátních, spárovaných a odmítnutých snímků a sentinel pro prázdné porovnání je nyní 0. Přidán regresní test bez shodných bodů. `go test ./internal/xiaomi/ptzprobe -count=1` a konzolové `go test ./... -count=1` prošly.
- Aktuální Windows x64 binárky jsou konzole SHA-256 `0EFC2AFEA3AB58950FF03129AABE89F87AF46177FCF31506CE1D8A37AA7FDF65` a helper SHA-256 `7B08F89E2D79488A8AD5C6A6D32A170ABED477EFD02A8F3B7F6EC53ECB69CCD0`; helper pin ve spouštěči odpovídá. Konzole byla po sestavení načtena a odpovídá, PTZ je nečinná. Pozorovatelovo potvrzení fyzického otočení zatím není zaznamenáno.

## PTZ 0x113: návratové pole a offline porovnání, 2026-11-11

- Z obou existujících odpovědí 0x113 byly vyčteny pouze allowlistované číselné položky: první `ret=0 angle=47.94 elevation=14.3`, druhá `ret=0 angle=51.86 elevation=14.3`; položky `xAngle`, `yAngle`, `xStep`, `yStep` chyběly. Pole `angle` se mezi dvěma levými kroky změnilo o +3.92°, elevace zůstala stejná. Jde o kamerou vrácené stavové pole, které podporuje změnu polohy; kvůli 33minutovému odstupu a chybějícímu čtení před druhým krokem samo neurčuje, kdy přesně ke změně došlo.
- V datovém adresáři testu nejsou uložené obrazové snímky (`.jpg`, `.jpeg`, `.png`, `.pgm`, `.gray`, `.raw`): počet nalezených nedávných artefaktů je 0. Živý pokus proto nebylo možné zpětně přehrát offline.
- Přidán headless regresní test měření: statický obraz vrací `no_consistent_shift` (8/8 spárovaných, 0 px), známý vodorovný posun −8 px vrací `consistent_shift` (8/8 spárovaných, −8 px) a posun −20 px přes výchozí rozsah ±16 vrací `inconclusive` (0/8 spárovaných, 8 odmítnutých pro nedostatek shod). Testy `TestMeasureImageShiftReportsNoMatchFramesWithoutSentinel` a `TestMeasureImageShiftStaticKnownShiftAndBeyondSearchRange` prošly.
- Headless syntetické testy tedy rozlišují statickou scénu, rozpoznatelný posun v rozsahu a neprůkazný posun mimo rozsah. Skutečný běh dokončil sběr; během přechodu nenašel konzistentní posun a následné snímky neměl s čím spárovat. Další motorický povel nebyl odeslán.

## PTZ follow-up: širší offline porovnání a ukládání důkazních snímků, 2026-11-11

- Rozsah porovnání po zápisu se nyní zvětšil z ±16 px na ±24 px. Výchozí baseline, čerstvý snímek před zápisem a klidová stabilita dál používají původních ±16 px a limit driftu 2 px; rozšířený rozsah se používá jen při porovnávání snímků po povelu. Před zápisem se tím bezpečnostní práh neuvolnil.
- Headless Go testy prošly: statická scéna `no_consistent_shift` (8/8 spárovaných, 0 px), známý posun −8 px `consistent_shift` (8/8, −8 px), posun −20 px `consistent_shift` (8/8, rozsah 24 px) a posun −32 px `inconclusive` (0/8 spárovaných). Plochý snímek bez použitelných shod také vrací `inconclusive` s nulovým počtem spárovaných snímků a bez sentinelové hodnoty. Předzápisový test odmítl posun −20 px jako nestabilní, takže jeho rozsah zůstal 16 px. `go test ./internal/xiaomi/ptzprobe -count=1 -v` prošel za 28,8 s.
- Ukládání a offline přehrání důkazů prošlo regresním testem včetně validace proti path traversal. Zkompilovaný Windows x64 helper byl spuštěn nad syntetickým offline záznamem: baseline stabilní, 8 výchozích a 8 klidových snímků, rozsah 24 px, `consistent_shift`, 8/8 shod a `shift_x_px=-20.00`; příkaz neprovedl síťový požadavek.
- Helper nyní při pokusu, který dojde k zápisu, uloží výchozí a klidové grayscale snímky do privátního adresáře `private\ptz-frame-captures\ptz-capture-*` a vypíše pouze ID a počty snímků. Snímky lze následně přehrát offline parametrem `-replay-capture-dir`. Změněný Windows x64 helper je nainstalován do cesty používané konzolí; SHA-256 `1C12FCCA8D94A44B565F95B5FA310AF2726EB68EFE82BF6CEB068C8F5416AAA0`. Předchozí helper se SHA-256 `7B08F89E2D79488A8AD5C6A6D32A170ABED477EFD02A8F3B7F6EC53ECB69CCD0` zůstává vedle něj jako záloha. `scripts/Prepare-Test-Camera.ps1` nyní pinne nový hash.
- Cílené testy bezpečnostních bran, RTSP fronty a managed HTTP helperu prošly. Plná sada helperu má jednu neprocházející textovou kontrolu `TestRunbookOrdersPostStartGateBeforeInitializerAndReadiness`: test očekává přesně dvě volání inventory GET ve vybraném bloku dokumentovaného runbooku, ale v textovém bloku aktuálního `diagnostika/C201_E1_PREDANI_FINAL.md` nachází čtyři. Tato dokumentační kontrola nesouvisí s měřením obrazu; runbook jsem neměnil.
- Konzolové testy `go test ./... -count=1` prošly (2,9 s). PowerShell parser spouštěče prošel a jeho `ExpectedProbeHash` se shoduje s nainstalovaným helperem.
- V tomto pokračování nebyl odeslán další živý motorický povel. Předchozí jediný potvrzený krok má stále neprůkazné obrazové potvrzení; nový helper je připraven pro další jednotlivé kliknutí přes běžné potvrzení konzole, až uživatel toto kliknutí výslovně potvrdí. Dosud není doloženo, že se kamera při předchozím kroku fyzicky otočila.

## PTZ follow-up: shodná validace offline a živých oken, 2026-11-11

- Offline přehrání nyní před analýzou obrazu kontroluje stejné podmínky oken jako živý běh: baseline i klidové okno musí mít přesný konfigurovaný počet (8) snímků, nenulové a přísně rostoucí časové značky a délku 3–5 s. Baseline i klidové okno pak musí projít stejnou stabilitní kontrolou s rozsahem ±16 px, přísnou většinou shod a maximálním driftem 2 px. Posun po povelu se vyhodnotí až po stabilním klidovém okně a dál používá rozšířený rozsah ±24 px. `PTZ_REPLAY` vypisuje stav a důvod obou oken i jejich délku.
- Přidány headless regresní testy pro neúplnou baseline, neúplné klidové okno, okno kratší než 3 s a nestabilní klidový obraz. Všechny vadné případy vracejí `image_status=inconclusive` a nespustí klasifikaci posunu. Platný záznam zůstává `consistent_shift` pro −20 px. `go test ./internal/xiaomi/ptzprobe -count=1` prošel za 28,0 s.
- Výsledný nainstalovaný Windows x64 helper byl spuštěn headless nad syntetickými záznamy: platný záznam měl baseline/quiet `stable`, 8 snímků a 3500 ms v každém okně a vrátil −20 px; čtyři klidové snímky vrátily `quiet_status=incomplete quiet_reason=frame_count`; osm snímků s driftem 3 px vrátilo `quiet_status=unstable quiet_reason=excess_drift`. Oba neplatné záznamy měly `image_status=inconclusive` a nula spárovaných snímků. Offline replay nevytvořil žádný síťový požadavek.
- SHA-256 nového helperu: `2D3ECA76B86F22A75E4F0874FD01790C5227A4F48981D1D58CDCACF7462C10E0`; hodnota v `scripts/Prepare-Test-Camera.ps1` odpovídá a PowerShell parser prošel. Předchozí helper `1C12FCCA8D94A44B565F95B5FA310AF2726EB68EFE82BF6CEB068C8F5416AAA0` zůstal jako záloha `managed-ptz-http-probe.before-window-gates-20261111.exe`.
- Kompletní testy `ptzprobe`, konzole a cílené bezpečnostní testy helperu prošly. Známá samostatná textová kontrola `TestRunbookOrdersPostStartGateBeforeInitializerAndReadiness` stále očekává dvě inventory GET ve výřezu runbooku, kde nachází čtyři; tento dokumentační test v tomto kroku nebyl měněn.
- V tomto pokračování nebyl odeslán živý PTZ povel. Dřívější jediný zápis 0x112 a odpověď 0x113 stále nemají časově přiřazený obrazový ani pozorovatelský důkaz skutečného otočení. Příští živý krok musí proběhnout jednou přes standardní potvrzení konzole; snímky z téhož pokusu se pak uloží pro offline replay.

## Obsazený port konzole a stav živé gateway, 2026-11-11

- Konzole nyní zkusí konfigurovanou loopback adresu a při chybě `address in use` otevře volný port na stejné loopbackové adrese. Skutečnou URL vypíše až po úspěšném bindu. `TestServerFallsBackFromBusyLoopbackPortWithoutStoppingOwner` ověřuje fallback i to, že původní listener zůstane nedotčen; `TestListenConfiguredKeepsFreePreferredLoopbackAddress` ověřuje zachování volného výchozího portu.
- Spouštěč před startem konzole hledá vlastníka portu 18880. Ukončí proces stromem pouze při přesné shodě cesty k projektovému `bin\diagconsole.exe` a aktuálnímu Windows účtu; pokud Windows vlastníka skryje, vyžaduje navíc stejnou interaktivní relaci. Proces z jiné relace nebo s jinou cestou ponechá běžet a konzole použije náhradní loopback port. PowerShell parser prošel. `Get-Process -IncludeUserName` může u jiného PID vyžadovat vyšší oprávnění; tato chyba je zachycena a přípravu konzole již nezastaví.
- Aktuální Windows x64 build prošel skriptem `Build-Windows.ps1`, včetně `go test ./... -count=1`; SHA-256 `545544CDDA2629703E68724F7A370FD58905C36BB6684B99ADF59F9CA143FBE2`.
- Skutečný EXE byl spuštěn s obsazeným náhodným loopback portem `50469`; po úspěšném bindu vypsal náhradní URL `http://127.0.0.1:50470`. Následný HTTP GET z tohoto prostředí skončil `SocketException`, takže přístupnost route skutečného procesu přes HTTP zde ověřit nešlo. Handler a mock status jsou pokryté průchozími Go testy.
- Port 18880 se při kontrole nedal bindnout, ale `netstat` neukázal listener PID a neběžel žádný `diagconsole.exe`. Nebyl tedy nalezen vlastní proces, který by šlo ukončit; žádný proces nebyl zabit. Konzole má v tomto případě přejít na náhradní loopback port.
- V omezeném pracovním kontextu testovací gateway neběžela. Nejnovější soukromý log uváděl pouze bezpečně filtrovaný výsledek `account_restore result=error reason=dns`. DNS dotazy z tohoto kontextu selhaly pro `account.xiaomi.com` i `de.api.io.mi.com` (`SocketException`). Pozdější ověření v běžném Windows kontextu však našlo existující ověřenou gateway; viz následující sekce.
- PTZ povel nebyl odeslán; tento běh ověřoval pouze startovací cestu.

## Závěrečný start konzole na výchozím portu, 2026-11-11

- Opakované spuštění `Start-Test-Camera.cmd` bez argumentů našlo běžící ověřenou gateway. Při obsazeném portu 18880 skript ukončil pouze projektovou konzoli se stejnou cestou k EXE a v téže interaktivní relaci (`OWN_CONSOLE_ON_18880=stopped_same_session`); ostatní procesy by ponechal běžet. Následná konzole úspěšně nabindovala výchozí adresu `http://127.0.0.1:18880`.
- Headless HTTP ověření aktuálního EXE: `GET /` → HTTP 200; `GET /api/status` → HTTP 200, `mock=false`, `gateway.info_status=200`, `ptz.enabled=true`. Gateway API zůstalo ověřené a loopback-only.
- Pro ověření nebyl obnoven inventář, nebyl odeslán PTZ požadavek a kamera se nepohybovala. Konzole i již běžící testovací gateway zůstaly spuštěné pro použití uživatelem.

## Opětovné spuštění při obsazeném portu bez zastavení původní konzole, 2026-11-11

- `scripts/Prepare-Test-Camera.ps1` už neprohledává port kvůli ukončování procesu a nevolá `taskkill`; vytvoří živou konfiguraci a pokračuje. `README.md` popisuje, že při obsazeném portu 18880 původní proces zůstane běžet a nová konzole použije náhradní loopback port.
- Před testem odpovídala původní konzole na `127.0.0.1:18880` s PID `27636`. Bez argumentů byl spuštěn celý `Start-Test-Camera.cmd` proti již ověřené gateway. Nová instance po úspěšném bindu vypsala `http://127.0.0.1:50481`.
- Po startu zůstal listener na 18880 pod stejným PID `27636`; jeho `/api/status` stále vrátil HTTP 200. Nová URL vrátila `GET /` HTTP 200 a `GET /api/status` HTTP 200 s `mock=false`, `gateway.info_status=200` a `ptz.enabled=true`.
- Příprava použila existující gateway, API zůstalo loopback-only. Inventář nebyl obnoven a PTZ povel nebyl odeslán. Původní konzole na 18880 i nová konzole na 50481 zůstaly spuštěné.
- PowerShell parser prošel; kontrola potvrdila odstranění `taskkill` a funkce pro ukončování konzole ze spouštěcího skriptu.

## Přímé PTZ ovládání C201 ve čtyřech směrech, 2026-11-12

- Konzole Windows x64 byla sestavena přes `scripts/Build-Windows.ps1` a `go test ./... -count=1` prošel; SHA-256 konzole `4AFB2B71221F1312C5A8B6E09042A2509F41CD045C4E1D1B9B448F6535A591DC`. Aktualizovaný gateway build má SHA-256 `316227F676E0A07A1565D234A5C1EC8E00BE9E877ACFE12A208D64C1F85EE374`; tento hash je připnutý v `scripts/Prepare-Test-Camera.ps1`.
- Gateway byla kontrolovaně restartována přes `Start-Test-Camera.cmd`. Nový run `host-run-20261112-010046-d54d48ac`, PID `5412`, běží z `xlg-ptz-console.next.exe`, start `2026-11-12T00:00:46.714Z`; API `127.0.0.1:53022`, `api_exposed=false`, health 200. Firewall se neměnil. Konzole PID `38588` obsluhuje `http://127.0.0.1:51930/`; GET `/` vrátil 200, stránka obsahuje všechny čtyři šipky a JS neobsahuje potvrzovací dialog.
- Inventář vrátil čtyři kamery a jednu C201. Ta se vybrala automaticky a její session dosáhla stavu `streaming`. API token ani RTSP URL nebyly zobrazeny ani zapsány do reportu.
- Z každého směru proběhlo přes konzoli jedno kliknutí a jeden krok bez automatického opakování. Všechny kroky měly `cmd_sent_id=0x112`, `write_status=write_ok`, `reply_status=reply_received`, `reply_cmd_id=0x113` a `late_reply_count=0`:

| Směr | Čas UTC | Latence odpovědi | Pozorování |
| --- | --- | ---: | --- |
| Vlevo | `2026-11-11T23:19:41.524Z` | 625 ms | Snímek se posunul opačně než pohyb kamery, očekávaně pro pohyb vlevo. |
| Vpravo | `2026-11-12T00:05:14.340Z` | 171 ms | Svislý okraj se posunul doleva, očekávaně pro pohyb kamery vpravo. |
| Nahoru | `2026-11-12T00:05:54.871Z` | 122 ms | Záběr ukazoval téměř hladkou stěnu; svislý pohyb z něj nebylo možné spolehlivě určit. |
| Dolů | `2026-11-12T00:06:50.548Z` | 134 ms | Stejné omezení scény; odpověď gateway potvrzuje zápis a odpověď, ne fyzický směr. |

- UI zobrazuje `Odesláno`; `0x113` a fyzický pohyb zůstávají v UI správně označené jako neměřené. Vizuálně jsou potvrzené levý a pravý směr; fyzický směr nahoru/dolů zůstává na této scéně neověřený.
- `node scripts/Test-Preview-UI.js` prošel (čtyři směry, výběr C201, nula volání `confirm()`); `scripts/Test-Mock.ps1` prošel na izolovaném portu a všechny čtyři směry pouze simuloval bez gateway požadavků. Cílené gateway/provider/protokolové Go testy prošly.
- Náhled byl po pozorování zastaven; live konzole zůstává otevřená na `http://127.0.0.1:51930/`, session připravená a PTZ tlačítka dostupná. Snímky nebyly uloženy do repozitáře.
## Vazba PTZ kliknutí na C201 v kartě a ověření svislých směrů, 2026-11-12

- `POST /api/ptz/step` nyní nese `camera_id` zobrazené v dané kartě. `PTZ.RunOnce` před přípravou i zápisem používá totožné ID. Jestliže se sdílený výběr změní během přípravy, handler vrátí HTTP 409 s chybou `camera selection changed before PTZ step; no request was sent`; kompatibilní `/api/ptz/left` zůstává dostupný.
- Nový regresní test se dvěma C201 A/B vnutí změnu výběru na B během přípravy kroku požadovaného pro A. Test prošel: výsledkem je 409 a nula PTZ POSTů na A i B. Cílené `go test ./internal/console -run 'PTZ|GatewayRequiresExplicitChoice' -count=1 -v` prošlo. `node scripts/Test-Preview-UI.js` prošel a ověřil stejné ID C201 ve všech čtyřech PTZ požadavcích; `scripts/Test-Mock.ps1` prošel na izolovaném portu, včetně čtyř simulovaných směrů a kompatibilního levého endpointu, bez gateway požadavků.
- Windows x64 build `scripts/Build-Windows.ps1 -GoPath C:\Apps\go\bin\go.exe` dokončil plné `go test ./... -count=1` a sestavení. SHA-256 konzole `CA3A0816E787382DC2C242CB2E775CC43ECBFC37334920419BCD3D209EACB1F3`. Nová konzole servírovala `http://127.0.0.1:53796/` (HTTP 200); tlačítka posílala explicitní ID karty.
- Gateway run `host-run-20261112-013455-33a9af70`, PID `41616`, start `2026-11-12T00:34:55.865Z`, běžel z připnutého buildu `xlg-ptz-console.next.exe` (`316227F676E0A07A1565D234A5C1EC8E00BE9E877ACFE12A208D64C1F85EE374`). API `127.0.0.1:50466` zůstalo loopback-only (`api_exposed=false`, health 200); firewall se neměnil.
- Z nové konzole proběhl jeden živý klik nahoru a jeden dolů. Oba měly `cmd_sent_id=0x112`, `write_status=write_ok`, `reply_status=reply_received`, `reply_cmd_id=0x113` a `late_reply_count=0`:

| Směr | Čas UTC | Latence odpovědi | Pozorování |
| --- | --- | ---: | --- |
| Nahoru | `2026-11-12T00:36:53.399Z` | 144 ms | Dolní roh rámu po kroku zmizel za spodní hranou; obraz se posunul dolů, odpovídá naklopení kamery nahoru. |
| Dolů | `2026-11-12T00:37:30.577Z` | 123 ms | Stejný roh se vrátil výše do záběru, opačný posun odpovídá naklopení dolů. |

- Tím jsou čtyři fyzické směry pozorovány: vlevo/vpravo v předchozím záznamu, nahoru/dolů zde. Náhled byl po ověření zastaven; live konzole zůstala otevřená s připravenou C201 session a dostupnými šipkami. Žádné API tokeny ani RTSP URL nejsou v tomto záznamu.

## Závěrečný build a konzole připravená pro použití, 2026-11-12

- Po fyzickém ověření byl v capability exploreru stav PTZ aktualizován na `live_verified`. Závěrečný Windows x64 build prošel `scripts/Build-Windows.ps1` včetně plných Go testů; SHA-256 konzole `EAB4029EA8513557C3B428F8DC3BA8BBC6D1A4F1E46EB9DF34EC4D46438D9497`.
- `Start-Test-Camera.cmd` spustil čerstvý gateway run `host-run-20261112-014534-1c636e7f`, PID `33036`, API `127.0.0.1:53252`, health 200, `api_exposed=false`. Proces běží z `xlg-ptz-console.next.exe` se SHA-256 `316227F676E0A07A1565D234A5C1EC8E00BE9E877ACFE12A208D64C1F85EE374`; firewall se neměnil.
- Finální konzole PID `39716` běží na `http://127.0.0.1:63983/` (HTTP 200). Stránka načetla inventář, připravila jednu C201 session, ukazuje stav `LIVE VERIFIED` a všechny čtyři aktivní šipky. Náhled zůstává vypnutý; po tomto spuštění nebyl odeslán další pohybový příkaz. Předchozí fyzické směrové testy jsou v předchozí sekci.

## Párování náhledu s vybranou C201, 2026-11-12

- RTSP URL se nyní odvozuje jen z aktuálně vybrané připravené C201 v managed inventáři. Cache předchozí kamery byla odstraněna; starý klíč `rtsp_url_file` se kvůli nepárovatelnosti ignoruje. Obnova inventáře i změna výběru nejprve synchronně zastaví náhled. Pokud worker do 5 s neskončí, server vrátí HTTP 503 a inventář/výběr nezmění. Poslední snímek se po zastavení smaže a MJPEG spojení se ukončí. PTZ zůstává nezávislé na běžícím náhledu.
- Cílené Go testy prošly: `TestGatewayRequiresExplicitChoiceForMultipleC201AndKeepsTheirStreamsPaired` ověřuje, že po nejednoznačné obnově ani při nepřipravené C201 nelze použít předchozí RTSP URL či legacy soubor; `TestSelectionAndInventoryRefreshStopPreview` ověřuje zastavení workeru a odstranění starého snímku při přepnutí i refreshi; `TestPTZStepForCameraBWorksWithPreviewOff` ověřuje právě jeden Bearer-autentizovaný POST na C201 B při vypnutém náhledu; regresní test změny výběru během PTZ přípravy zůstává bez zápisu. `TestMockLeftPTZNeedsActionHeaderAndSendsNoGatewayRequest` i nový mock lifecycle test potvrzují nula požadavků z mocku na gateway.
- `node scripts/Test-Preview-UI.js` prošel nad skutečnými `index.html`, `app.css` a `app.js`: starý MJPEG zdroj se při výběru i obnově ihned vyčistí a skryje, všechny čtyři šipky posílají ID zobrazené C201 a dialog se neotevírá.
- Windows amd64 konzole sestavena do `bin/diagconsole-selection-safe.exe`, SHA-256 `0EFC71E937AB4B085B476CECA64ABC7BD68E1A67EE8E99A8D96F40D7709A685A`; build metadata potvrzuje `GOOS=windows`, `GOARCH=amd64`, `CGO_ENABLED=0`. Původní `bin/diagconsole.exe` byl zamčen běžícím procesem a nebyl přepsán. `Build-Windows.ps1` zablokovalo systémové pravidlo nepodepsaných PowerShell skriptů; ekvivalentní přímý `go build` proběhl úspěšně bez změny execution policy.
- V tomto ověřovacím běhu nebyl odeslán žádný fyzický PTZ krok; předchozí záznam čtyř fyzických směrů zůstává platný.

## Odmítnutí zastaralého PTZ požadavku a aktuální Windows launcher, 2026-11-12

- `Gateway.PrepareCamera` už nepřepíná sdílený výběr. Zastaralé `camera_id` se odmítne před přípravou session. Výběr, obnova inventáře a příprava session jsou serializované společným zámkem; `Gateway.Step` drží tentýž zámek od kontroly ID až do dokončení autentizovaného POSTu. Souběžný výběr nebo aplikace nového inventáře proto nemůže změnit cíl mezi kontrolou a zápisem.
- `TestStalePTZRequestLeavesSelectedCameraAndPreviewUntouched` prošel: při výběru B a běžícím preview B dostal opožděný požadavek pro A HTTP 409, nevznikl žádný gateway request, výběr zůstal B a preview i poslední snímek B zůstaly aktivní. `TestPTZStepRejectsCameraSelectionChangeDuringPreparation` dál ověřuje změnu výběru během přípravy bez zápisu. `TestPTZStepForCameraBWorksWithPreviewOff` ověřil jediný Bearer-authenticated POST pro B a to, že změna výběru čeká do dokončení zápisu. `TestPTZStepSerializesWithInventoryRefresh` ověřil stejné pořadí proti aplikaci obnoveného inventáře. Mock PTZ test neprovedl žádný gateway request. Cílená Go sada prošla.
- `node scripts/Test-Preview-UI.js` prošel; šipky nadále posílají ID zobrazené C201 a starý obraz se při přepnutí/obnově skryje.
- Build skript a `Start-Test-Camera.cmd` nyní používají `bin/diagconsole-current.exe`, takže zamčený starší `bin/diagconsole.exe` není překážkou. Windows amd64/CGO off build má SHA-256 `F1C94B4E7E8F9D2D9F55360B676BE174DECE34213E35D2BB18A9110A2C3478B2`. Lokální execution policy nepovolila přímé spuštění nepodepsaného `Build-Windows.ps1`; sestavení proběhlo ekvivalentním přímým `go build` a cílené Go testy byly spuštěny samostatně.
- `Start-Test-Camera.cmd` byl spuštěn se samostatnou mock konfigurací, aby tento HTTP smoke test nevolal gateway ani kameru. Konzole vypsala `http://127.0.0.1:62955`; GET `/`, `/app.js` a `/api/status` vrátily HTTP 200, status potvrdil `mock=true` a odpovědi obsahovaly aktuální hint i ID payload. Testovací proces byl po ověření ukončen.
- Fyzické směry nebyly opakovány; předchozí záznam čtyř ověřených směrů zůstává platný.

## Serverem potvrzený výběr po obnově inventáře, 2026-11-12

- `renderInventory` nyní vždy přebírá `selected_camera_id` ze serveru. Prázdný výběr ve vícekamerovém inventáři vymaže dřívější lokální ID; zobrazí se placeholder a PTZ šipky zůstávají zakázané, dokud server nepotvrdí novou volbu.
- `node scripts/Test-Preview-UI.js` prošel novým scénářem: po refreshi se dvěma C201 a prázdným výběrem je lokální ID prázdné, placeholder viditelný, šipky zakázané a pokus kliknutí nevytvoří PTZ POST. B lze znovu vybrat; následný klik vygeneroval právě jeden požadavek s `camera_id` B.
- Kompletní konzolová sada `go test ./... -count=1` prošla. Windows amd64/CGO off build `bin/diagconsole-current.exe` má SHA-256 `08F734EB7A08056208C1B6713FA037D69EBC023D92C470E29F391EA06259085D`.
- `Start-Test-Camera.cmd` byl spuštěn se samostatnou mock konfigurací: loopback `http://127.0.0.1:50613`, GET `/`, `/app.js` a `/api/status` vrátily HTTP 200. Status potvrdil `mock=true`; doručený JS obsahoval pravidlo pro autoritativní serverový výběr i povolení šipek pouze při vybrané C201. Testovací proces byl poté ukončen.
- Nebyl odeslán fyzický PTZ krok; předchozí protokol čtyř ověřených směrů zůstává platný.

## Single-flight polling statusu při pomalé gateway, 2026-11-12

- UI spouští nejvýše jeden `/api/status` request současně. Další poll tiky a dokončené akce se sloučí do jednoho navazujícího requestu po odpovědi; každá odpověď stále musí projít kontrolou `statusRequestID` a `inventoryRevision`. Tím pomalé odpovědi nevyhladoví UI a stará revize inventáře nemůže přepsat novější výběr.
- `node scripts/Test-Preview-UI.js` prošel s dvěma řízeně zadrženými status odpověďmi modelujícími 5 s latenci vůči 3,5 s intervalu. Test vložil jeden poll tick během každé z nich, ověřil nejvýše jeden současný status HTTP požadavek, následný aktuální status v UI a znovupovolení šipek po dokončení PTZ. Ověření po refreshi zadrženou kamerou B z předchozí revize zůstává průchozí.
- Kompletní `go test ./... -count=1` v modulu konzole prošel. Windows amd64/CGO off `bin/diagconsole-current.exe` má SHA-256 `E7E21F847C31EC253750D5678CBB2DEDE4D643CDA3B9EB825F9C7E81374D7E63`.
- `Start-Test-Camera.cmd` byl spuštěn s mock konfigurací na `http://127.0.0.1:65388`: GET `/`, `/app.js` a `/api/status` vrátily HTTP 200; `mock=true`; JavaScript obsahoval single-flight frontu statusu a zpracování navazujícího pollu. Testovací proces byl ukončen.
- Fyzické PTZ směry nebyly opakovány.

## Odmítnutí zastaralých status odpovědí UI, 2026-11-12

- `status()` nyní při zahájení zachytí pořadové číslo požadavku a revizi inventáře. Odpověď se použije jen pokud je stále nejnovější, revize výběru se od té doby nezměnila a inventář není právě v obnově/výběru. Chyby zastaralých odpovědí také nemění UI stav. Odpovědi inventáře se obdobně aplikují jen pro aktuální akci.
- `node scripts/Test-Preview-UI.js` prošel novým závodem: test zadrží status se serverovým výběrem B a běžícím náhledem, dokončí obnovu inventáře s prázdným výběrem a pak doručí starou odpověď B. Výběr zůstane prázdný, náhled skrytý, šipky zakázané a před opětovnou volbou nevznikne PTZ POST; po opětovném výběru B jeden klik odešle jeden krok pro B.
- Kompletní sada konzole `go test ./... -count=1` prošla. Windows amd64/CGO off build `bin/diagconsole-current.exe` má SHA-256 `AF530CFE475E00068341DA951B17C0BC232B6449FF4D616A1E35EEB3C01CD330`.
- `Start-Test-Camera.cmd` byl ověřen se samostatnou mock konfigurací na `http://127.0.0.1:57251`: GET `/`, `/app.js` a `/api/status` vrátily HTTP 200; status měl `mock=true` a doručený JS obsahoval ochranu podle `statusRequestID` i `inventoryRevision`. Testovací proces byl po ověření ukončen.
- Fyzické PTZ směry nebyly opakovány.

## Dokončení pomalého pollingu bez čekání na další cyklus, 2026-11-12

- Po dokončení status odpovědi se sloučený navazující cyklus spouští odděleně (void status()), takže promise právě dokončeného volání nečeká na jeho další pomalou odpověď. Zachováno je nejvýše jedno aktivní /api/status, jeden čekající cyklus a kontrola pořadí požadavku i revize inventáře.
- node scripts/Test-Preview-UI.js prošel v čase 2026-11-12T07:42:49.958Z. Dva zadržené status response modelovaly latenci 5 000 ms při intervalu pollingu 3 500 ms. Test ověřil, že první status() skončí, zatímco druhá odpověď zůstává zadržená, současně neběží více než jeden status požadavek, následná odpověď aktualizuje UI a po PTZ kroku se šipky znovu povolí. Regrese staré odpovědi z předchozí revize inventáře a opětovný klik pro B zůstaly průchozí.
- Kompletní go test ./... -count=1 v modulu konzole prošel. Windows amd64/CGO off build bin/diagconsole-current.exe má SHA-256 A5BE2E31253B4836EF20C4F9101A377CE5CD8D754761CA16C2989CC46A56284D.
- Start-Test-Camera.cmd byl spuštěn přes CMD se samostatnou mock konfigurací na http://127.0.0.1:64849. GET /, /app.js a /api/status vrátily HTTP 200; API potvrdilo mock=true. Servírovaný JavaScript obsahoval single-flight kontrolu i oddělené spuštění void status(). Testovací proces a dočasná mock konfigurace byly po ověření odstraněny.
- Žádný fyzický PTZ krok ani gateway požadavek nebyl proveden.

## Průzkum dalších funkcí: živý náhled a SD karta, 2026-11-12

- Živý status na http://127.0.0.1:63983 potvrdil režim live, dostupnou gateway (info HTTP 200), vypnutý náhled i nahrávání. Jedno explicitní spuštění náhledu vrátilo started; status ukázal 3 snímky v rozlišení 848x480. Zastavení vrátilo stopped a navazující status potvrdil video.running=false. Během ověření nebyl poslán PTZ požadavek, obnoveno inventory ani spuštěno nahrávání.
- Read-only GET /api/capabilities, /api/events a /api/record/files odpověděly HTTP 200. Capability stav SD zůstává unknown; seznam lokálních souborů obsahoval 0 záznamů a nahrávání neběželo. Záznamy nebyly otevřeny ani staženy.
- Navazující plán K5 požaduje zjistit dostupnost SD kapacity, read-only výpisu a výběru záznamu. Oficiální [specifikace C201](https://www.mi.com/global/product/xiaomi-smart-camera-c201/specs/) potvrzuje podporu MicroSD 8–256 GB; [stránka produktu](https://www.mi.com/global/product/xiaomi-smart-camera-c201/) zmiňuje přehrávání záznamů z karty/Xiaomi Home. Tyto stránky nedokládají lokální API/protokol.
- Lokální Gateway nemá routu pro SD ani capability úložiště v modelu Camera. Nižší vrstva Xiaomi streamu obsahuje Session.Recordings(channel), které posílá read-only index request a vrací start/délku/event klipu; nevrací kapacitu. Metoda není zapojená do provider/API a z kódu ani offline fixtures není ověřen storage channel konkrétní C201. Vhodný další návrh je modelově omezené provider API pro read-only index s offline fixture testy; kapacitu držet unknown, dokud nebude samostatně doložena. Neznámé MIoT zápisy ani živý index probe v tomto běhu provedeny nebyly.

## Důkazy pro C201 SD funkce a návrh K5, 2026-11-12

| Funkce | Doloženo v místních záznamech | Možnost kódu | Neověřeno |
|---|---|---|---|
| Stav karty a kapacita | MIoT specifikace pro xiaomi.camera.c201 definuje SIID 3 Memory Card Management: PIID 1 Status (uint8 0–100), PIID 2 total, 3 free a 4 used (uint32 0–2147483648). Všechny jsou read+notify, bez write; schéma neuvádí jednotku ani mapování statusu. Diagnostická sekvence 2026-11-06 přečetla SIID 1 / PIID 4 Current Firmware Version = 5.3.2_0016 a následně SIID 3 / PIID 1–4 s code=0 a raw hodnotami 1, 0, 0, 0. | Account.MiotGetContext v cloud API umí číst vlastnosti s kontextem, ale není zapojen do storage provider/API. | Jednotky, význam Status=1 a nulových kapacit, současná přítomnost karty a reálná celková/volná kapacita. Nuly se nesmí vydávat za bajty ani důkaz chybějící karty. |
| Read-only výpis | Místní MIoT odpovědi nedokládají RDT index ani storage kanál. | Session.Recordings(channel) čte RDT IndexCommand 6 a parsuje start, délku a příznak události. | Storage kanál C201 a formát platného prázdného indexu; gateway/provider endpoint chybí. |
| Výběr / načtení klipu | V místních záznamech není C201 fetch ani playback klipu. | Session.FetchRecording používá přesný index start a channel. FileRequest nyní vytváří 24bajtový payload s timestampem v bytes 0–3 a kanálem little-endian v bytes 8–11. | C201 přijetí a odpověď souboru; přesný timestamp a kanál v reálném C201 požadavku. Rozšířený obecný builder ještě není důkazem podpory na C201. |

- Diagnostika svázala specifikaci, C201 a firmware přes MIoT čtení, ale kapacitní hodnoty jsou pouze historické raw odpovědi bez jednotek či dekódování statusu. Oficiální podpora MicroSD 8–256 GB je rozsah podporovaných médií, nikoli údaj o kartě v této kameře.
- Návrh v docs/C201_SD_READONLY_DESIGN.md nyní zahrnuje read-only GET MIoT vlastností, zachování raw hodnot, oddělený RDT index, valid-empty versus timeout/channel_unverified a výběr klipu přes přesný index start.
- `FileRequest` nyní posílá 24 bajtů, timestamp v bytes 0–3 a kanál little-endian v bytes 8–11. Test `internal/xiaomi/protocol/recordings/recordings_test.go` kontroluje délku, přesný timestamp, nenulový kanál a nulová rezervovaná pole. Nezávislá PowerShell kontrola zdrojového tvaru a stejného little-endian rozložení prošla. Dne 2026-11-12 prošel cílený test příkazem `C:\Apps\go\bin\go.exe test ./internal/xiaomi/protocol/recordings -count=1` (Go 1.27.3, windows/amd64): `ok`, 0.487 s. To ověřuje formát generického požadavku, nikoli přijetí 24bajtového požadavku na živé C201; její podpora ani storage kanál zůstávají neověřené.
- Místní diagnostický ZIP obsahoval MIoT specifikaci a komunikaci JSON; surový RDT/PCAP záznam nebyl nalezen. Žádná živá sonda, stažení ani zápis nebyly v tomto kroku provedeny.

## Další schopnost: read-only stav detekce, 2026-11-12

Lokální C201 MIoT specifikace obsahuje tyto čitelné vlastnosti:

| SIID.PIID | Vlastnost | Typ / přístup |
|---|---|---|
| 5.1 | Motion Detection | bool, read/write/notify |
| 5.2 | alarm-interval | uint8, read/write/notify, minuty 1–30 |
| 5.3 | Detection Sensitivity | uint8, read/write/notify, bez zdokumentovaného rozsahu |
| 8.6 | Human Detection | bool, read/write/notify |
| 8.7 | Move Detection | bool, read/write/notify |
| 8.31 | Move Detection Sensitivity | uint8, read/write/notify, bez zdokumentovaného rozsahu |

Historický MIoT read v diagnostice z 2026-11-06 vrátil pro tyto položky `code=0`: 5.1=false, 5.2=10, 5.3=0, 8.6=true, 8.7=false, 8.31=0. Další čtené přepínače byly 8.3 Pet Detection=false a 8.22 pet-tracking=false. Jde o uložené hodnoty z dané diagnostické relace, ne o aktuální snapshot ani důkaz doručení motion eventů. Schéma u citlivosti neuvádí rozsah a záznam neobsahuje pohybovou událost.

MIoT specifikace dále deklaruje v SIID 8 AI Detection události: IID 3 Pet Appeared (argumenty 9 a 12), IID 4 Object Motion (bez argumentů) a IID 5 Someone Appeared (argumenty 2 a 12). Jde o schéma modelu, ne důkaz, že firmware 5.3.2_0016 tyto události skutečně vyšle nebo že je cloud zpřístupní této integraci.

Komunikační záznam z 2026-11-06 obsahuje MIoT property reads, vyjednání transportu a živou media/RDT diagnostiku; neobsahuje odběr MIoT událostí ani event payload. Gateway cloud klient implementuje `/miotspec/prop/get`, `/miotspec/prop/set` a `/miotspec/action`, ale nebyla nalezena cesta pro subscription/event delivery. Přístup `notify` v property schématu proto nepovažujeme za ověřený stream událostí.

Další ověřitelný podkrok je určit z dokumentace/protokolu Xiaomi mechanismus doručování těchto tří událostí a případně offline model jejich payloadů. Fyzické vyvolání události ani změna detekčních přepínačů zatím neproběhly. Historické property values z 2026-11-06 zůstávají oddělené od aktuálního stavu kamery.

## Další schopnost: noční režim, read-only podklady, 2026-11-12

MIoT specifikace xiaomi.camera.c201 definuje Camera Control SIID 2 / PIID 3 `Night Shot` jako uint8 s přístupem read/write/notify a výčtem `0=Off`, `1=On`, `2=Auto`. Stejná služba deklaruje události IID 1 `IR Turned On` a IID 2 `IR Turned Off`. Diagnostický záznam z 2026-11-06 má pro SIID 2 / PIID 3 odpověď `code=0`, hodnota `0`, tedy podle schématu tehdejší režim Off.

To potvrzuje čitelnost vlastnosti v historické relaci a význam enumu ve specifikaci. Neříká to aktuální nastavení, skutečné fyzické přepnutí IR filtru ani doručení IR událostí. Stejně jako u detekce kód obsahuje obecné MIoT property get/set/action, nikoli odběr událostí. Pro bezpečný další krok stačí read-only snapshot `2.3`; žádná změna režimu ani živé čtení v tomto průzkumu neproběhly.

### Jednorázový pokus o aktuální read-only čtení, 2026-11-12 11:29 UTC

Před odesláním dotazu byla zkontrolována existující lokální relace bez otevření desktopu nebo zahájení přihlášení. Nebyl nalezen proces diagnostické konzole/gateway ani listener na známé URL `127.0.0.1:63983`; `netstat` také neukázal listener na tomto portu a `Get-Process` nenašel proces s názvem odpovídajícím `diagconsole`, `xiaomi`, `gateway`, `go` nebo `camera`. Doplňující čtení command line přes CIM skončilo `AccessDenied`.

Gateway nemá routu pro MIoT property read; v kódu je pouze metoda `Account.MiotGetContext`, která vyžaduje existující autorizovaný objekt `Account`. Protože dostupná autorizovaná relace nebyla nalezena, aktuální read `SIID 2 / PIID 3` nebyl odeslán a nová relace nebyla vytvořena. Aktuální režim zůstává neověřený. Historický read z 2026-11-06 (`code=0`, hodnota `0` = Off podle specifikace) zůstává oddělen; fyzický IR stav ani doručení událostí nejsou ověřeny.

## Privacy režim, lokální průzkum, 2026-11-12

V uložené MIoT specifikaci C201 nebyla nalezena vlastnost ani událost označená privacy, privacy mode nebo lens cover. Specifikace obsahuje Camera Control SIID 2 / PIID 1 `Switch Status` (bool, read/write/notify); historické čtení z 2026-11-06 vrátilo `code=0`, `true`. Název ve specifikaci neoznačuje soukromou závěrku, proto tuto hodnotu nelze interpretovat jako privacy stav ani fyzické zakrytí objektivu. Aktuální stav zůstává neznámý; žádné přepnutí nebylo provedeno.

## Další čitelná funkce: kontrolka, lokální podklady, 2026-11-12

MIoT specifikace obsahuje Indicator Light SIID 4 / PIID 1 `Switch Status` (bool, read/write/notify). Diagnostický read z 2026-11-06 vrátil `code=0`, `true`, což podle názvu property dokládá tehdejší zapnutý stav přepínače kontrolky; fyzické rozsvícení ani aktuální stav ověřeny nejsou. Není k dispozici aktuální autorizovaná relace, takže nový read se neodeslal. Žádný write nebyl proveden.
