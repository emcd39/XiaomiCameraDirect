# Další krok: diagnostika fyzického C201

## Aktuální stav — 7. 11. 2026

- **L2 technicky splněno přes samostatný Windows gateway RTSP klient.** Nový běh obnovil Xiaomi EU účet, našel inventář čtyř kamer včetně C201, dokončil CS2 LAN search/punch, otevřel `cs2+tcp` a přijal H.265.
- FFmpeg 9.0.2 dekódoval pět minut do `null`: 5 937 snímků, 848×480, exit 0. Tři nová RTSP připojení dekódovala po pěti snímcích a skončila exit 0; každý celý pětisnímkový pokus trval přibližně tři sekundy. Čas do prvního snímku nebyl samostatně měřen. Souhrn: `.test-env/evidence/test-c201-l2-20261107-093852-dee0e478.log`.
- Obraz nebyl uložen ani ručně zobrazen. Na začátku FFmpeg hlásil chybějící HEVC reference; dostupný log neurčuje proč. Připojení uprostřed GOP před keyframem je jen hypotéza.
- Krátký pokus o fyzické HA HLS 2026-11-07 10:01 CEST se zastavil ještě před spuštěním HA: WSLc/WSL vrátily `E_ACCESSDENIED`, HA port 8123 neposlouchal. `camera/stream` nebylo vyžádáno a síťová pravidla se nezměnila. Záznam: `.test-env/evidence/test-c201-ha-hls-attempt-20261107.log`.
- **Nejbližší krok:** po obnovení místního WSL/HA runtime zkusit krátký `camera/stream` HLS decode do `null`; potom vizuálně potvrdit obraz v cílovém klientovi. PTZ vyžaduje dohled; zvuk zůstává vypnutý, dokud nebude zvolen krátký živý audio test.
- Fyzické PTZ, talkback, WAN nezávislost, přímá LAN versus relay a cílový Linuxový HA běh na stejné IoT LAN zůstávají neověřené. Xiaomi provider dál deklaruje `control=unsupported`.

Starší položky níže jsou chronologický záznam pokusů a jejich výsledků; jejich historické „další kroky“ nepřebíjejí tento aktuální souhrn.

## Běhové prostředí a důvod použití Windows

Windows se používají i pro cílenou diagnostiku na výslovné přání uživatele, protože notebook je na stejné Wi-Fi jako C201 a Xiaomi Viewer profil je uložený ve Windows. Token profilu chrání DPAPI; testovací skript jej lokálně rozšifruje, dočasně předá gateway s omezenými ACL a nikdy jej nevypisuje do evidence. DPAPI není závislostí gateway ani cílové integrace. Cílem zůstává Linux HA.

Gateway už běží jako Linuxový program: [Dockerfile](../Dockerfile) sestaví statický Go binární soubor `xlg` a spustí jej v Alpine Linux kontejneru. `scripts/test-c201-ha.ps1` sestaví tento image z aktuálního checkoutu a spouští jej vedle čistého Home Assistant kontejneru. Jde o izolovaný test přes WSLc NAT, nikoli instalaci do hlavního HA OS. Dočasná Windows gateway je jen diagnostický nástroj; není cílovým během.

**Izolovaný HA kamerový tok prochází; fyzický C201 nyní splnil samostatné L2 dekódování, ale jeho obraz přes HA HLS ověřen není.** Regresní běh `.test-env/evidence/test-local-20261106-213224-8efdb456.log` (`RESULT=PASS`, exit 0) použil maketu: poskytla dekódovaný JPEG, 12 sekund/60 snímků HLS přes HA, 5 sekund přímého RTSP, čtyři PTZ požadavky a obnovu po restartu gateway. To není ověření fyzické kamery.

Linuxový fyzický běh `.test-env/evidence/test-c201-ha-20261106-214107-d3bd6870.log` obnovil účet, načetl čtyři kamery a vytvořil HA entitu C201, ale oba pokusy skončily ve fázi `cs2_lan_search_timeout`. Předchozí cloudový timeout v `.test-env/evidence/test-c201-ha-20261106-213619-439c822e.log` nastal ještě před HA a není důkazem chyby kamery. V době těchto pokusů fyzický obraz nebyl ověřen; následný přímý Windows běh dekódoval stream podle L2 kritéria. Linuxový HA HLS a PTZ zůstávají neověřené.

Windows běh po opravě CS2 transportu (`.test-env/evidence/test-c201-cs2-20261106-224428-4e082a73.log`) dokončil LAN search i UDP punch, otevřel relaci a přijal H.265. V tomto běhu se ještě nepodařilo snímek dekódovat; pozdější L2 běh je zaznamenán v aktuálním souhrnu výše. HA HLS se tímto ani pozdějším během neověřilo.

Kód bezpečně rozliší timeout v CS2 LAN search, UDP punch, autentizaci a startu media příkazu bez logování IP nebo surové chyby. Stejný čtyřbajtový UDP search paket z Windows hostitele i WSLc NAT také timeoutoval; Windows hostitelský výsledek je sám o sobě neprůkazný, protože testovací proces nemá vlastní inbound výjimku. Čtecí dotaz na WSL Hyper-V firewall skončil `Client neměl přístup k prostředku CIM`, takže stav pravidel není ověřený. Připravený helper `scripts/c201-wsl-firewall.ps1` je pouze **volitelný experiment pro WSLc NAT**, nikoli povinná součást instalace ani podmínka pro nativní Linux HA. Jeho syntaxe prošla PowerShell parserem; pravidlo zatím přidáno nebylo.

První běh `.test-env/evidence/test-c201-ha-20261106-211111-88cf3e3a.log` sestavil a spustil Linuxovou managed gateway i čistý testovací HA, nastavil integraci a našel entitu `xiaomi.camera.c201`. HA `camera_proxy` vrátil HTTP 500; JPEG se nezískal. Druhý běh `.test-env/evidence/test-c201-ha-20261106-211425-5ee2b4f1.log` opět našel entitu, ale přímé RTSP z HA do gateway nenačetlo snímek. Sanitizovaný gateway log potvrdil `account_restore`, inventář se čtyřmi kamerami a `session_negotiate` přes CS2; výsledek `session_open` ani první média se do konce pokusu nezapsal. Třetí běh `.test-env/evidence/test-c201-ha-20261106-211605-31e9fa19.log` skončil při HA config flow `cannot_connect`, bez gateway eventů. Aktuální runner proto před config flow ověřuje health/inventář přímo z HA a pak nechává RTSP diagnostiku opakovat do 55 sekund, aby zachytil výsledek otevření média.

`scripts/test-c201-ha.ps1` čte přihlášený profil z Windows DPAPI pouze proto, že je to jediné dostupné uložené přihlášení. Token rozšifruje pod stejným Windows uživatelem, zapíše jej do dočasného privátního env souboru s omezeným ACL, předá jej Linuxové gateway a soubor po startu kontejneru smaže; do evidence se tajná hodnota nepíše. Tento přenos není součástí Linuxového produktu. WSLc síťování je NAT, takže fyzický výsledek popisuje tuto kontejnerovou trasu, nikoli nativní HA OS ani Linux se síťovým rozhraním přímo v IoT LAN.

C201 má ověřené samostatné L2 dekódování fyzického streamu. Před úplným potvrzením cílové podpory zbývá vizuálně zkontrolovat obsah a ověřit HA HLS, desetiminutový stream a obnovu relace. Fyzické PTZ se zaznamená zvlášť; současný integrační model jej pro tohoto poskytovatele neexponuje.

## Zjištění z 6. 11. 2026

- Předem nakonfigurované heslo bridge bylo použito pouze v paměti pro lokální autentizaci; nebylo vypsáno ani uloženo. Health potvrdil propojený účet.
- Inventář C201 hlásil `online=true`, `lan_online=false`, zvolenou cestu `compat`, `publishable=true`, bez chyby konfigurace zdroje a bez naměřených FPS. V oficiálním SDK session seznamu nebyla aktivní relace C201.
- Jeden čerstvý snapshot s `max_age=0` přes lokální control API vrátil `503`; bezpečná klasifikace odpovědi označila selhání RTSP zdroje. JPEG ani dekódovaný snímek nevznikl. Nastavení kamery se neměnilo. Sanitizovaný výstup `wslc logs --tail 600` neobsahoval žádné řádky.
- Ubuntu-24.04 WSL2 běží, ale používá virtuální podsíť oddělenou od LAN kamery. Není to prostředí pro požadované srovnání z Linuxu na stejné LAN. Stávající bridge nabízí jen `official` a `compat`; samostatné vynucení přímého P2P nebo relay není zdokumentováno.

## Dřívější požadavek: přímý Linux v IoT LAN

Předchozí Linuxový pokus potřeboval test hosta v IoT LAN, protože dostupný Ubuntu-24.04 WSL2 měl `eth0` `192.168.26.141/20` a routu ke C201 přes `192.168.16.1`, nikoli on-link přes `192.168.3.0/24`. Windows notebook na `MoukaHome_IoT` následně potvrdil fyzické L2 dekódování. Cílová architektura zůstává Linux Home Assistant; přímý nativní Linuxový test v IoT LAN stále není k dispozici. Další kroky: vizuální potvrzení obrazu, fyzická HA HLS cesta, desetiminutové přehrávání a obnova relace; fyzické PTZ se řeší samostatně.

## Zpřístupnění notebooku do IoT Wi-Fi, 6. 11. 2026

Notebook nyní používá `192.168.3.178/24` na stejné podsíti jako C201 (`192.168.3.119`). Windows ping na kameru prošel. Čerstvý snapshot přes běžící bridge však stále vrátil HTTP 503 s klasifikací chyby RTSP zdroje, bez JPEG. Uvnitř bridge kontejneru vede trasa přes WSL gateway `172.17.0.1` a TCP port 554 na kameře odmítl spojení. To potvrzuje, že připojení notebooku k IoT Wi-Fi samo o sobě nepřesunulo kontejner do stejné síťové cesty. K ověření z Linuxu přímo na LAN by bylo potřeba zrcadlené síťování WSL nebo samostatný Linux host. Zatím se nezměnila konfigurace WSL.

Následné krátké TCP testy portu 554 provedly stejné spojení z obou prostředí. Windows použil `Wi-Fi`, zdroj `192.168.3.178` a on-link trasu `192.168.3.0/24`. Bridge kontejner použil `eth0`, zdroj `172.17.0.2` a gateway `172.17.0.1`. Obě spojení skončila `ConnectionRefused` přibližně po 2,1 s. Pro port 554 tedy změna síťové cesty výsledek nezměnila. Odmítnutí portu neříká nic o použitelnosti RTSP zdroje poskytovaného go2rtc. Další diagnostika má sledovat spuštění zdroje v bridge a sanitizovaně zaznamenat fázi RTSP `DESCRIBE` včetně toho, zda zdroj vrátí SDP. `mirrored` by mělo smysl ověřovat pro případné další porty/protokoly zdroje, pokud se ukáže, že kontejnerová trasa k nim selhává.

Srovnávací test portu 554 je dokončen. Dne 6. 11. 2026 v 15:40 UTC proběhl jeden časově korelovaný snapshot z přihlášeného bridge přes stávající WSL NAT, bez změny nastavení. Efektivní log level go2rtc v `/data/go2rtc.yaml` je `info`; proces předává sloučený stdout/stderr bridge loggeru a ten jej zapisuje do stdout kontejneru. Soubor add-on options v kontejneru přítomen nebyl, takže nadřazenou hodnotu nastavení nelze ověřit. Načtení inventáře před pokusem timeoutovalo, proto požadavek použil existující lokální C201 entitu; její identifikátor se do záznamu nezapsal.

Log bridge v 15:40:45 UTC zaznamenal začátek čtení publikovaného streamu. V 15:40:53 UTC go2rtc zapsalo varování, že UDP čtení zdroje skončilo časovým limitem. Čtení preview skončilo v 15:41:04 UTC. Časově odpovídající snapshot vrátil HTTP 503 s RTSP `DESCRIBE 404`; SDP nepřišlo a nevznikl JPEG ani dekódovaný snímek. Odečet go2rtc API ukázal jen nakonfigurovaný zdroj, bez runtime stavu a médií. Log potvrzuje, že se čtení zdroje spustilo a skončilo UDP timeoutem. Příčina timeoutu zůstává nezjištěná. Záznam vynechává ID kamery, URL zdroje a surové řádky logu.

Windows snapshot API vrátilo HTTP 503 za 14 ms, protože na Windows není dostupný FFmpeg; to není selhání CS2. Linuxový FFmpeg z cached HA image se při pokusu připojit k RTSP na Wi-Fi IP notebooku setkal s `connection_refused`; firewall jsme neměnili. Další krok je dekódovat stejný RTSP zdroj přes klienta, který má povolenou cestu k Windows relayi, a následně ověřit živé HLS v HA. Fyzické PTZ zůstává nepodporované a neověřené.

### Trace pokus po ověření konfigurace, 6. 11. 2026

Běžící kód snapshotu v kontejneru byl ověřen při požadavku; FFmpeg měl argument `-rtsp_transport tcp`. Před jedním dalším snapshotem jsem dočasně nastavil `rtsp: trace` a `xiaomi: trace` při zachování základní úrovně `info`. Poté jsem obnovil původní `/data/go2rtc.yaml` bajt po bajtu, restartoval go2rtc a ověřil původní úroveň `info` i nepřítomnost trace override.

Požadavek začal v 15:46:34 UTC a vrátil HTTP 503 s `DESCRIBE 404`; FFmpeg nedostal SDP a nevznikl snímek. Korelovaný log potvrzuje začátek preview a události `DESCRIBE`. Trace obsahuje SDP payload marker, ale bez směru či transaction ID jej nelze přiřadit k odpovědi na tento `DESCRIBE`; použitelná SDP odpověď proto potvrzená není. UDP událost obsahuje loopback adresu, ale vzdálený endpoint ani socket nelze určit. V tomto trace pokusu není explicitní timeout řádek. Předchozí běh na úrovni `info` časově koreloval UDP timeout, ani ten neobsahoval protistranu. Výstup neobsahuje identifikátory, zdrojové URL ani SDP payload.

Poslední izolovaný regresní běh je `.test-env/evidence/test-local-20261106-121819-f540d7c5.log`: Go race/vet, HA konfigurace/entit, čtyři mock PTZ směry a obnova prošly. Běh nekomunikoval s fyzickou kamerou.
# Direct Windows C201 attempt, 6 Nov 2026

The signed-in Xiaomi Viewer profile was restored by the current checkout's Windows build without printing or persisting the Xiaomi token. The gateway health endpoint returned HTTP 200, account restore succeeded, and cloud inventory returned four cameras including one C201. CS2 negotiation succeeded, but opening the media session ended with a timeout. No decoded frame or JPEG was produced. This run was outside the WSLc network, so WSLc NAT alone does not explain the remaining failure.

The [Xiaomi Camera Viewer documentation](https://github.com/lubos-hnanicek/xiaomi-camera-viewer/blob/main/README.md#windows-firewall) explains that CS2 replies arrive on a temporary UDP port from a different source port, and Windows Firewall must permit the receiving executable. The Viewer has its own app rule; the local `xlg.exe` pilot does not. Creating a narrowly scoped rule for the pilot executable was attempted from the current account and Windows returned access denied because administrator rights are required. A local helper is prepared under `.test-env/c201-pilot/win-gateway/firewall.ps1`; it scopes UDP to the C201 address and the notebook Wi-Fi address, and TCP API/RTSP to the WSLc bridge subnet and the two pilot ports. It supports `-Action Remove` for cleanup.

After the CS2 fix, the current Windows build restored the account, found four cameras, completed the UDP punch, authenticated/opened media, received H.265, and reported RTSP SDP ready. The snapshot endpoint returned HTTP 503 because Windows has no FFmpeg executable. A cached Linux HA FFmpeg client could not reach the Windows RTSP port (`connection_refused`); no firewall rule was changed. Physical decoding, HA HLS, recovery, and PTZ remain unverified.

### Latest diagnosis and next action, 6 Nov 2026

Windows is only the diagnostic host: it is directly on `MoukaHome_IoT`, and the already signed-in Xiaomi Viewer token is protected by that Windows user's DPAPI profile. The local pilot reuses it without exposing the token. The target remains the Linux Home Assistant gateway.

The corrected build now completes CS2 search and punch, opens the media session, receives H.265, and prepares RTSP SDP. This confirms delivery of media to the Windows gateway, not a decoded picture. Windows lacks FFmpeg; the Linux HA decoder was refused when it tried the Windows RTSP endpoint. No firewall change has been made. Physical image, HA HLS, recovery, and PTZ remain unverified. The provider still does not expose physical PTZ.

The pilot firewall helper consumes the active run's dynamic API/RTSP port record. On 7 Nov, the user confirmed UAC; the scoped helper recorded the temporary rules as added and then removed. Its elevated WSLc FFmpeg call failed with `RemoteException`, so the rule effect was not measured. Do not leave or add another firewall rule for this result; see the latest physical attempt below.

The later same-host probe found no portable Windows `ffmpeg.exe` and no `XLG_FFMPEG_PATH`. The cached HA image's FFmpeg 8.1.2 ran successfully. With RTSP configured on `0.0.0.0:59487`, a Windows self-connect to the notebook's Wi-Fi address succeeded, but the container source `172.17.0.3` received `connection_refused`; no frame decoded. `host.docker.internal` is not resolvable in this WSLc image, so tests must use the actual notebook address. This does not prove the firewall is the cause. The elevated retry waited at UAC and was interrupted; firewall state after it is unverified. No firewall conclusion or physical image result is claimed. Sanitized record: `.test-env/evidence/test-c201-local-path-20261106-233156-19cabe9a.log`.


## Fyzický C201 — běh před L2 dekódováním, 7. 11. 2026

- Uživatel potvrdil UAC. Dočasná firewall pravidla byla zaznamenána jako `added` a poté `removed`; pravidla nezůstala přidaná. Jejich vliv na přenos nebyl ověřen, protože zvýšený WSLc FFmpeg skončil `RemoteException`.
- Aktuální Windows gateway běžela přímo na notebooku v IoT Wi-Fi. Xiaomi účet/inventář, CS2 LAN search, punch a `session_open` uspěly; loopback RTSP klient se připojil. Gateway pak zaznamenala `media_read reason=protocol` a relace skončila před `first_media`. FFmpeg dekódoval 0 snímků. Záznam: `.test-env/evidence/test-c201-local-frame-20261107-074335-8730b374.log`.
- Pokus rozlišit `EOF`/`unexpected EOF` od ostatních protokolových chyb nebylo možné sestavit ani otestovat: `wslc build --target go-race` skončil ještě před buildem chybou `E_ACCESSDENIED`; lokální `go.exe` není k dispozici. `go vet` v tomto pokusu nebyl spuštěn. Neověřená změna klasifikátoru byla vrácena. Výpis překážky: `.test-env/evidence/test-c201-eofclass-checks-20261107.log`.
- Poslední potvrzená fyzická fáze zůstává otevření CS2 relace, nikoli přijetí média. Živý obraz/HLS, obnova, desetiminutový běh i fyzické PTZ nejsou ověřené; Xiaomi provider PTZ stále nenabízí. Mock HLS a mock PTZ jsou samostatné testy.
- Další technický krok: znovu zpřístupnit WSLc testovací build nebo poskytnout Go toolchain a pak opakovat race/vet a jeden headless běh. Pro ověření HA HLS na cílové linuxové síti je nadále potřeba Linuxový HA/gateway se síťovou cestou do IoT LAN.

## Headless retry after access check, 7 Nov 2026

- No Go toolchain was found in PATH or common Windows install locations. Standard `wsl.exe --list --quiet` also stopped with `Wsl/E_ACCESSDENIED`; no alternate Linux Go toolchain was available.
- One no-firewall loopback probe using the existing current-source executable stopped at Xiaomi `account_restore`, sanitized reason `dns`, before inventory, CS2, or FFmpeg. Evidence: `.test-env/evidence/test-c201-local-frame-20261107-075455-c59b6051.log`; the private gateway log was reduced to `account_restore result=error reason=dns`. The gateway process exited. No camera packet or control command was sent in this attempt.
- Therefore the latest C201 protocol result remains the 07:43 attempt: CS2 session opened, then `media_read reason=protocol` before the first media packet; 0 decoded frames. Neither physical HLS nor PTZ has been verified.
- Resume after Xiaomi endpoint DNS/account restore works and Go/WSL test execution is available. Do not infer a new camera result from the DNS failure.

### DNS scope check, 7 Nov 2026

The gateway's Xiaomi EU shard is `de.api.io.mi.com`; saved-token restore first contacts `account.xiaomi.com`. A direct PowerShell `Resolve-DnsName` check for both names failed locally with Win32 NativeError 5 (`Access denied`). This is a sandbox permission failure, not an NXDOMAIN result; actual endpoint DNS from the notebook therefore remains unverified. The headless gateway attempt reported `account_restore reason=dns`, but the local execution restriction may affect that path too. No repeat was made.

## CS2 read-path inspection and diagnostic change, 7 Nov 2026

- Static inspection found why `reason=protocol` may hide a clean channel close: `cs2.Conn.Error()` returns its worker error when set, but returns `io.EOF` when channels close without one. `miss.Client.ReadPacket()` wraps that error; the provider's previous classifier treated an unrecognized EOF as generic `protocol`. This is a code-path explanation, not proof that the 07:43 camera run ended with EOF.
- `tcpConn.Read()` uses `io.ReadFull`, so a truncated 8-byte envelope/body can surface as `io.ErrUnexpectedEOF`. The CS2 reassembler can also fail with `pop buffer is full`; oversized TCP framing reports `cs2: tcp buffer too small`.
- Added sanitized classifications for `eof`, `unexpected_eof`, `media_buffer_full`, and `transport_frame_too_large`, plus focused classification test cases. These source changes are **not built or tested**: no Windows Go toolchain is installed; both `wslc build` and standard `wsl.exe --list --quiet` returned `E_ACCESSDENIED`. The existing Windows executable does not contain the new classifications.
- DNS checks for `account.xiaomi.com` and `de.api.io.mi.com` returned Win32 NativeError 5 (`Access denied`), not NXDOMAIN. The 07:54 `account_restore reason=dns` may therefore be an execution restriction. No second camera attempt was made.
- Once a Go build/test path and Xiaomi account restore work in an authorized local execution context, run the focused classifier tests, `go test -race ./...`, `go vet ./...`, then one loopback C201 probe. Do not claim EOF or packet corruption until the rebuilt binary reports it.

## Aktuální Windows loopback diagnostika, 7. 11. 2026

- Dočasný Go 1.27.3 toolchain v `.test-env` prošel kontrolou oficiálního SHA-256. `gofmt` exit 0, `go test ./internal/xiaomi/...` exit 0 po spuštění v rozsahu povolujícím loopback UDP, `go vet ./internal/xiaomi/...` exit 0 a Windows build aktuální gateway exit 0. Úplný záznam: `.test-env/evidence/test-c201-go-diagnostic-20261107.log`.
- `go test -race ./internal/xiaomi/...` se nespustil: Windows Go má `CGO_ENABLED=0` a v systému není GCC/Clang. Nemá výsledek pass; cílené běžné testy ano.
- Headless test aktuálního buildu úspěšně obnovil účet, načetl 4 kamery, dokončil CS2 LAN search/punch a otevřel session. Vyjednaný transport byl `cs2+tcp`. Následné čtení vrátilo skutečné `io.EOF`, relace skončila před prvním mediálním paketem; FFmpeg přes loopback dekódoval 0 snímků. Sanitizovaný běh: `.test-env/evidence/test-c201-local-frame-20261107-081708-b37f86d9.log`; důvod a transport byly ověřeny z privátního gateway logu bez vypsání payloadu.
- Jde o konkrétní závěr na transportní vrstvě: TCP peer zavřel CS2 spojení před videem. Příčina, proč peer po `StartMedia` zavírá, zůstává neznámá. HLS ani fyzické PTZ C201 nejsou ověřené.
- Evidence filtr byla upravena, aby bezpečně zachovávala i `+` v hodnotě `cs2+tcp`; PowerShell parser prošel. Další diagnostika má porovnat payload/příkaz `StartMedia` s funkčním Xiaomi Viewerem nebo zachytit jen hlavičky TCP FIN/RST a časové pořadí; plný payload nezapisovat do běžných logů.

## Direct C201 profile and control probe, 7 Nov 2026

- Saved Xiaomi Viewer configuration reports `quality=hd`, primary channel, automatic transport, and audio enabled. The Go probe intentionally kept audio disabled.
- With audio disabled, Go opened a CS2 session but received no video for profiles 0 through 5. Auto transport selected `cs2+tcp` and returned EOF; forced `cs2+udp` at profile 2 remained open for 10 seconds but received zero video frames, bytes, media headers, replies, and unhandled channel messages. No decoder received media. Sanitized evidence: `.test-env/evidence/test-c201-live-probe-20261107.log`.
- One `left` MotorStep was sent exactly once through the authenticated TCP session. MIoT position readback `siid=6/piid=12` was unavailable before/after; a separate read-only query returned Xiaomi error `-704040003`, so movement could not be independently verified. Do not expose Xiaomi PTZ in HA yet.
- The owner approved one short audio-enabled diagnostic. It opened `cs2+tcp` at HD/quality 2 and produced 5 H.265 video access units plus 10 audio packets; audio was counted, cleared from memory, never played or saved. The probe stopped after the fifth video unit before its 10-second cap. Audio-off attempts had no video, suggesting `enableaudio=1` changes the camera response, but no JPEG has been decoded yet. This approval does not authorize ongoing microphone use in HA. Next, request approval for one more short audio-enabled session that decodes video in memory and discards audio.
