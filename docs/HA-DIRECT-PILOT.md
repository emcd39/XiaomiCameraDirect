# Xiaomi Camera Direct – záznam testů

Provozní nastavení nativního testovacího Home Assistantu, start instance, porty a cesta k panelu jsou zapsané v [runbooku testovacího HA](HA-TEST-ENV.md).

## Kontrolní bod testovacího HA — 20. 11. 2026

Uložená kopie konfigurace je `.test-env/ha-native-win-py314/checkpoints/2026-11-20-known-good`. Manifest pokrývá 69 souborů; hashe sedí a 13 chráněných položek DPAPI se úspěšně obnovilo pod stejným účtem Windows. Postup obnovy a umístění souborů karet popisuje README v kontrolním bodu. Kopie je lokální a ignorovaná Gitem; necommitovat.

Obnova do oddělené kopie prošla v připnutém Home Assistant Container `2026.10.4`: `check_config` exit 0, autentizované API načetlo dashboardy `xiaomi-direct` a `xiaomi-live`, přímá karta obsahovala všech pět očekávaných kamer a `/local/xiaomi-live-camera.js?v=1` i `/webrtc/webrtc-camera.js?v=v3.6.1` vrátily HTTP 200. Sanitizované logy jsou `.test-env/ha-native-win-py314/restore-container-check-config-20261120.log`, `restore-container-api-20261120.log` a souhrn `restore-smoke-preparation-20261120.log`. Validační kopie měla vypnuté Xiaomi položky; tímto smoke testem se nepotvrzují aktivní entity ani živý přenos z kamer. Dočasná dešifrovaná kopie a její kontejner byly po ověření odstraněny; šifrovaný kontrolní bod zůstal zachovaný.

Původní běžící/native testovací HA se při této kontrole neozval na portu 56264 (`API 000`). Zámek jeho konfigurace nebyl změněn ani odstraněn. Kontrolní bod je ověřená obnova dashboardů a zdrojů, nikoli tvrzení, že původní runtime právě běží.

## Aktuální stav fyzických kamer — potvrzení uživatele, 20. 11. 2026

| Kamera | Živý obraz | Fyzický pohyb PTZ |
| --- | --- | --- |
| C201 | **Funguje** | **Ověřen a funguje** |
| C400 | **Funguje** | **Ověřen a funguje** |
| C701 | **Funguje** | **Ověřen a funguje** |
| Mi 360 | **Funguje** | Přímé PTZ zůstává otevřené. [Následný izolovaný gateway/konzolový běh](LOCAL-CAMERA-PROGRESS.md#mi360-one-left-isolated-run-2026-11-15) poslal jeden krok doleva a snímky ukázaly odpovídající posun; nejde o přejímku PTZ přímé integrace ani všech směrů. |
| Outdoor AW300 | **Otevřeno — ověříme později** | Pevná kamera, PTZ se nepřejímá |

Jde o nejnovější potvrzený uživatelský stav a přepisuje starší výsledky v tomto záznamu, kde C201/C400/Mi 360 vracely 503 nebo kde fyzický pohyb nebyl ještě ověřen. Staré sondy popisují stav své tehdejší sítě; nejsou aktuálním verdiktem o streamu. V tomto tahu nebyl proveden nový kamerový pohyb ani síťová sonda. Zpětná vazba dále ověřené funkce ve starších testech shrnuje níže.

### Audit dalších funkcí podle dostupných důkazů

| Funkce | Stav | Rozsah důkazu / mezera |
| --- | --- | --- |
| QR přihlášení, inventář, výběr a opětovná autorizace | Implementováno; regresně ověřeno | Simulované HA flow testy prošly. Účet v testovacím HA načetl kamerový inventář; změna sítě a obnovení každé položky se musí řídit čerstvým preflightem. |
| Snapshot a krátký záznam | Implementováno | Na přímé C201 jsou fyzicky uložené a dekódované JPEG a MP4. Akce jsou v dashboardu i u dalších kamer, ale tento průkaz se nerozšiřuje na jejich fyzické záznamy. |
| Rozlišení C400 | Implementováno; fyzicky ověřeno | Nízký 864×480 a plný 2560×1440 profil byly přehrány na C400. |
| PTZ směrový krok | Implementováno | Fyzický pohyb C201, C400 a C701 je potvrzen uživatelem. U Mi 360 [následný izolovaný běh přes gateway](LOCAL-CAMERA-PROGRESS.md#mi360-one-left-isolated-run-2026-11-15) doložil jeden krok doleva; přímé PTZ a ostatní směry Mi 360 zůstávají otevřené. Zoom, presety, limity a tracking nejsou ověřené. |
| Obnova při změně IP/trasy | Implementováno; simulovaná HA regrese prošla | Kamera bez platné trasy zůstane nedostupná a bez streamu; po nalezení nové trasy se stejná entita se stejným `entity_id` znovu zpřístupní a stream se obnoví. Pět testů skončilo `OK`: [log testu](../.test-env/ha-native-win-py314/test-ha-direct-stream-route-refresh-e2e-loopback.log). Jde o simulaci; fyzický výpadek a návrat sítě v reálném HA zůstávají otevřené. |
| Sdílení relace a obnova mediální relace | Implementováno; simulovaně ověřeno | E2E ověřuje souběh odběratelů a obnovu jedné nové relace. Fyzické zotavení po výpadku kamery/sítě v reálném HA zůstává otevřené. |
| Příjem zvuku z C201 | Částečně ověřeno | Fyzická sonda zachytila Opus a na testovací stanici se dekódoval zvuk. Přehrání v HA/WebRTC kartě potvrzené není. |
| Příjem zvuku z C701 | Odtlumení ověřeno v Codexu, slyšitelný poslech čeká na přejímku. | Vestavěná ikona WebRTC v MSE přepnula ztlumení oběma směry. Samostatná HLS sonda naměřila velmi slabou zvukovou stopu. |
| Hovor z HA do reproduktoru C701 | Ovládání načtené a povolené; fyzický přenos čeká na ruční zkoušku | Karta zobrazuje aktivní stav mikrofonu C701 a tlačítko „Podrž pro mluvení“. Kód otevírá mikrofon prohlížeče pouze při podržení; server má deadman 0,8 s a limit 30 s. Opus jde přes autentizovaný HA WebSocket a MISS kanál 3. Skutečné odeslání řeči do reproduktoru ještě nebylo ověřeno; browser musí při prvním použití dostat oprávnění k mikrofonu. |
| Talkback ostatních modelů | Neimplementováno | Ovládání reproduktoru se nezobrazuje mimo opt-in C701. |
| SD stav a kapacita | Gateway: implementováno, offline/HA regresně ověřeno; fyzické hodnoty otevřené | Jde o read-only cloud MIoT senzory gateway; není to hotová schopnost přímé integrace. SD index a přehrávání klipů chybí. |
| Pohybové události a Frigate | Simulované mapování ověřeno; přímý export do Frigate otevřený | MQTT testy gateway prošly. Přímý stabilní export, skutečný Frigate záznam/detekce a obnova po výpadku nebyly fyzicky převzaty. |
| Noční režim, privacy, LED, tracking a další modelové funkce | Neúplné / nepřevzaté pro přímý backend | Read-only nastavení C201 existují v gateway; přímá fyzická funkčnost a ostatní modely mají vlastní otevřené přejímky. |
| Stabilita vydání | Neověřena | Chybí 24hodinový běh, čistá instalace na cílovém HA OS a fyzický test obnovy po restartu/výpadku. |

### C701 zvuk a ovládání karty v testovacím HA — 20. 11. 2026

Starší headless log `.test-env/ha-native-win-py314/c701-audio-live-ui-check-20261120.log` zachycuje kartu `/xiaomi-direct/xiaomi-direct-camera.js?v=0.3.5`, profil **Se zvukem** přes MSE a vlastní ovládací prvky karty: před kliknutím `video.muted=true`; po kliknutí na **🔇 Zapnout poslech** `video.muted=false`, `paused=false`, `readyState=4`, obraz 3840×2160 pokračoval a popisek se změnil na **🔊 Ztlumit poslech**. Log tedy dokládá toto tlačítko ve starší revizi `0.3.5`, nikoli vestavěnou ikonu WebRTC Camera v obou směrech. Nedokládá aktuální revizi `0.3.5.1`.

Ve stejném starším logu bylo `🎙 Podrž pro mluvení` viditelné a povolené pod obrazem, v klidovém stavu (`aria-pressed=false`). Mikrofon se neotevřel a hovor se do kamery neposílal. Fyzický přenos hlasu do reproduktoru C701 zůstává otevřený.

Revize `/xiaomi-direct/xiaomi-direct-camera.js?v=0.3.5.1` byla ověřena v prohlížeči Codexu na panelu Xiaomi přímo → Kamery. Ikona reproduktoru přepnula `video.muted` v obou směrech při pokračujícím obrazu 3840 × 2160. Posuvník pod obrazem byl vyzkoušen na 99 % a vrácen na 100 %. Tlačítko hovoru je pod obrazem.

Čtyřsekundové měření přímého HLS streamu C701 z hostitele HA dekódovalo AAC-LC. FFmpeg volumedetect naměřil průměr −70,7 dBFS a špičku −50,1 dBFS (exit 0). Signál v okamžiku měření byl velmi slabý. Poslech při referenčním zvuku a fyzický hovor zůstávají otevřené.

## Výchozí stav z 16. 11. 2026

### Dynamický preflight kamerové sítě — 19. 11. 2026 22:57 UTC

Skener spuštěný z konfigurace lokálního testovacího HA vybral zdroj `192.168.3.178/24`, provedl pět cílených unicast sond CS2 bez retry a žádná kamera nebyla pro přímý přenos použitelná. C701 (`chuangmi.camera.079ae2`, `192.168.3.187`) je ve stejné podsíti, ale sonda timeoutovala. C201 (`xiaomi.camera.c201`), C400 (`chuangmi.camera.039a04`), Outdoor AW300 (`mxiang.camera.mod13`) a Mi 360 (`chuangmi.camera.026c02`) mají uloženou trasu přes `192.168.2.145/24`, mimo aktuální podsíť. Mi 360 byla dříve uživatelem nahlášena jako odpojená; tento běh nedokládá její fyzický stav. Úplný report: `.test-env/ha-native-win-py314/camera-reachability-current.json`. Tento okamžitý stav používáme pro výběr fyzických testů; před dalším fyzickým během stačí znovu spustit skener místo ručního opakování diagnostiky.

### Dynamický preflight kamerové sítě — 20. 11. 2026

Jednorázová sonda znovu vybrala zdroj `192.168.3.178`, poslala po jednom unicast CS2 paketu na každou z pěti kamer a žádná nebyla vyhodnocena jako použitelná pro přímý stream. C201, C400, Outdoor AW300 a Mi 360 mají kamerovou podsíť `192.168.2.0/24`, zatímco aktuální zdroj je mimo ni; jejich CS2 sondy timeoutovaly. C701 je v aktuální podsíti `192.168.3.0/24`, ale její CS2 sonda také timeoutovala. Report: `.test-env/ha-native-win-py314/camera-reachability-current-20261120.json`.

Tentýž běh potvrdil, že položka **Xiaomi Camera Direct** má pět registrovaných kamer. Hlavní dashboard `xiaomi-live` ale používal entity gateway. Přes lokální HA API byl přepnut na pětikamerové direct rozložení a obnoveny akce Snímek a Záznam (20 s), které míří na odpovídající direct entitu; 16 PTZ cílů míří na direct tlačítka. HA API potvrdilo všech pět kamer, oba dostupné skripty a uloženou konfiguraci. Report dashboardu je `.test-env/ha-native-win-py314/xiaomi-live-direct-final-20261120.json`; záloha původního dashboardu je `.test-env/ha-native-win-py314/xiaomi-live-before-direct-20261120.json`. Snímek ani záznam se v tomto ověření nespouštěly. V tomto dřívějším běhu se živý obraz neověřil; novější přehrávání C701 je zaznamenané níže.

### Odkaz na nativní direct dashboard — 20. 11. 2026

Pro běžící nativní testovací HA používej port `56264` z `.test-env/ha-native-win-py314/start-native-ha.ps1` a panel [Xiaomi přímo → Kamery](http://127.0.0.1:56264/xiaomi-direct/kamery). Odkaz na portu `56259` míří do jiné/proxy HA relace: tam runtime nabízí jen „Xiaomi kamery“ a cesta `/xiaomi-direct/kamery` vrací 404. Přítomnost `.storage/lovelace.xiaomi_direct` ani položky v uloženém registru sama o sobě nedokládá, že panel načetl běžící HA; ověř název v runtime postranním panelu.

V běžícím nativním panelu se potvrdilo pět video prvků a živý direct stream C701: 3840 × 2160, `readyState=4`, přehrávací čas 17,85 s. Ostatní čtyři karty hlásily `503 Service Unavailable`; HA log v 13:25 zaznamenal chybu CS2 handshaku u C201, C400, Outdoor AW300 a Mi 360. Hlavní dashboard „Xiaomi kamery“ zůstal nedotčený. Pro další ověření zachovej tento rozdíl mezi funkční trasou/C701 a nefunkčními streamy ostatních kamer; při uvádění stavu rozlišuj uloženou kartu od dekódovaného obrazu.

### Opakovaná kontrola běžícího HA — 20. 11. 2026

V otevřené relaci nativního testovacího HA se načetl panel **Xiaomi přímo → Kamery** na `http://127.0.0.1:56264/xiaomi-direct/kamery` (název runtime stránky: „Xiaomi přímo – Home Assistant“). Stránka obsahuje pět video prvků. C701 právě dekódovala živý obraz 3840 × 2160 (`readyState=4`, `paused=false`, přehrávací čas 60,7 s). Zbývající čtyři video prvky mají nulové rozměry a panel hlásí `503 Service Unavailable` (tři nabídky `webrtc/offer`, jedna `mse`). Hlavní dashboard **Xiaomi kamery** se při kontrole ani při opravě neměnil. Odkaz `127.0.0.1` je dostupný pouze z počítače, kde běží tato lokální testovací instance; port `56259` je jiná HA/proxy relace a direct panel v ní není zaregistrován.

Cílená jednorázová síťová kontrola ze stejného Windows hostitele a síťového prostoru jako nativní HA ověřila C201 (`192.168.2.124`) a C400 (`192.168.2.84`). Pro každou odeslala jeden unicast CS2/UDP paket na port 32108 s limitem 2 s, bez broadcastu a bez změny síťové konfigurace. Hostitel vybral zdroj `192.168.3.178`; obě uložené kamerové sítě jsou `192.168.2.0/24` a jejich stará nakonfigurovaná zdrojová adresa `192.168.2.145` na hostiteli neodpovídá zvolené trase. Obě sondy timeoutovaly a stav je `source_outside_camera_subnet`, `direct_usable=false`. Tato kontrola dokládá síťovou překážku na hostiteli, ne chybnou konfiguraci panelu.

V témže okamžiku panel vracel 503 i pro AW300 a Mi 360. Aktuální cílená route sonda tyto dva modely znovu neposílala; poslední společný preflight z 20. 11. 09:21 UTC zaznamenal pro oba stejný výběr zdroje mimo `192.168.2.0/24` a timeout CS2/UDP. Mi 360 byla uživatelem dříve označena jako odpojená. Podklady: `.test-env/ha-native-win-py314/c201-c400-route-current-20261120.json`, `.test-env/ha-native-win-py314/direct-ha-route-c201-c400-20261120-run.log` a `.test-env/ha-native-win-py314/camera-reachability-current-20261120.json`.

### Aktuální opakovaná kontrola — 20. 11. 2026

Panel byl znovu otevřen v lokální relaci prohlížeče na `http://127.0.0.1:56264/xiaomi-direct/kamery`. Karta C701 zobrazila živé video 3840 × 2160 (`readyState=4`, `paused=false`); čas videa postoupil z 1068,0 na 1069,2 s. Další čtyři karty nadále vracely 503. Samostatný headless záznam pro přesnou entitu `camera.xiaomi_c701_direct` už dříve dekódoval z její HLS cesty 100 snímků za 5 s videa, FFmpeg exit 0: `.test-env/ha-native-win-py314/d2-c701-direct-image.log`. Starší headless browser log `.test-env/ha-native-win-py314/headless-camera-final-20261119.log` uvádí URL panelu a rostoucí počet snímků, ale jeho pole entity u samotného video prvku bylo `null`; proto ho nepoužívat samotný jako důkaz mapování entity.

Aktuální seznam IPv4 rozhraní obsahuje Wi‑Fi `192.168.3.178/24` s výchozí bránou `192.168.3.1`; žádné rozhraní ani specifická trasa hostitele do `192.168.2.0/24` v tabulce není. Uložená konfigurace obou kamer stále uvádí zdroj `192.168.2.145`, který už hostitel nepoužívá. Jedna cílená CS2/UDP sonda na každou z C201 (`192.168.2.124`) a C400 (`192.168.2.84`) zvolila zdroj `192.168.3.178` a timeoutovala. Přímá integrace vyžaduje zdroj ve stejné kamerové síti; oprava pouze YAML/dashboardu proto není doložená. Pro obnovu C201/C400 je potřeba připojit hostitele k `192.168.2.0/24` s použitelnou adresou, případně nejprve rozšířit a otestovat podporu routovaného CS2 transportu.

AW300 a Mi 360 v tomto cíleném běhu znovu sondovány nebyly. Poslední společný preflight obou v `camera-reachability-current-20261120.json` měl stejnou síť `192.168.2.0/24`, zdroj `192.168.3.178` a timeout CS2. Mi 360 byla uživatelem dříve nahlášena jako odpojená. Pokus testovacího runneru o druhý start HA skončil chybou existujícího zámku konfigurace PID 16868; zámek nebyl mazán a konfigurace dashboardu se neměnila. Runner neměl z izolovaného shellu přístup k portu 56264, ale otevřená prohlížečová relace panel zobrazila. Záznamy: `.test-env/ha-native-win-py314/current-ha-routes-and-listener-20261120.log`, `.test-env/ha-native-win-py314/current-c201-c400-route-20261120.log`, `.test-env/ha-native-win-py314/current-native-ha-start-20261120.log`.

### Obnova inventáře při změně IP

Při úspěšné obnově účtu HA převezme pouze trasu nalezenou z aktuálních síťových rozhraní. Uložená `source_ip` a podsíť se nepoužijí jako fallback, protože stejná privátní IP se může opakovat v jiné síti. Pokud se aktuální trasa nenajde, kamera zůstane v inventáři, ale její přímé streamování zůstane vypnuté, dokud další obnova trasu nepotvrdí. Unit regrese ověřuje shodu i změnu IP. HA E2E pak obnoví stejnou položku nejprve bez trasy a poté s novou trasou: ověřuje nedostupnost a absenci zdroje v prvním stavu, obnovený stream a zachované `entity_id` v druhém. Cílená sada prošla pěti testy; log je `.test-env/ha-native-win-py314/test-ha-direct-stream-route-refresh-e2e-loopback.log`. Jde o simulované/offline ověření, nikoli o test fyzických kamer.

- Go reference: commit `518b500`, připnuté HA Container `2026.10.4`. Na dřívějším živém dashboardu byly přes Go ověřeny C201, Mi 360 a C400 souběžně. AW300 předala snímky jednou a další relace nedodaly první médium. Výsledky přímého režimu se evidují odděleně. [Záznam o kamerách](LOCAL-CAMERA-PROGRESS.md#c400-a-outdoor-aw300)
- Go MISS/CS2 obsahuje kód převzatý z go2rtc a Xiaomi Camera Viewer pod licencemi MIT. Při portu přenést oznámení pro převzaté části podle jejich skutečného původu. [Oznámení o původu](../internal/xiaomi/protocol/NOTICE.upstream.md) · [Go2rtc licence](../internal/xiaomi/protocol/LICENSE.go2rtc) · [Viewer licence](../internal/xiaomi/protocol/LICENSE.xiaomi-camera-viewer)
- Oficiální integrace Xiaomi Home dokládá autorizaci OAuth 2.0 a načtení inventáře. Tato dokumentace nepotvrzuje, že stejný token poskytne klíče pro C201 MISS média. Go reference řeší seznam zařízení a autorizaci MISS relace samostatnými operacemi `DevicesWithCameraModelsContext` a `MissVendor`. V D1 proto ověřit inventář a autorizaci médií odděleně. [Xiaomi Home](https://github.com/XiaoMi/ha_xiaomi_home/blob/main/README.md) · [Go reference](../internal/xiaomi/protocol/cloud/api.go) · [Diskuse C201 u go2rtc](https://github.com/AlexxIT/go2rtc/issues/2290)

## C201 preflight

Spusťte z **téhož síťového prostoru jako testovací HA**, například uvnitř testovacího HA Core kontejneru. Při HA OS spusťte zkušební běh přes testovací kontejner se stejnou síťovou cestou nebo přímo v Core, pokud máte přístup. Následující adresy jsou příklady; dosaďte IP své C201 a její skutečnou podsíť.

```sh
python3 scripts/ha_direct_preflight.py 192.168.50.5 192.168.50.0/24
python3 scripts/ha_direct_preflight.py 192.168.50.5 192.168.50.0/24 --probe
```

První příkaz pouze ověří zdrojovou IP vybranou systémem a neodesílá datagramy. Druhý pošle jeden CS2 LAN search přímo k vybrané IP a jeden na směrovaný broadcast této podsítě. Čeká na platný CS2 rámec z vybrané IP, bez autorizace, přístupu ke cloudu nebo otevření streamu. Jinou podsíť odmítne před odesláním. Výstup obsahuje výsledek a čas bez adres.

| Výsledek | Význam |
| --- | --- |
| `route_candidate` | Systém vybral zdrojovou IP v deklarované podsíti. Fyzická dostupnost kamery není potvrzena. |
| `cs2_lan_reply` | Z cílové IP přišla očekávaná CS2 odpověď. Další krok je navázání autorizované mediální relace. |
| `source_outside_camera_subnet` | Běh používá zdroj mimo deklarovanou podsíť. Změřte z HA připojeného do kamerové LAN. |
| `cs2_lan_search_timeout` | Přes zvolenou trasu během měření nepřišla platná odpověď. Zkontrolujte IP a dostupnost C201 a síťovou cestu před dalším pokusem. |

### Výsledek tohoto kroku

- **PASS** – pět rychlých offline ověření (cílová podsíť, NAT zdroj, režim bez odesílání, odmítnutí odpovědi jiné kamery a chybného rámce, timeout). `python -B -m unittest discover -s tests -p test_ha_direct_preflight.py`
- **BLOCKED** – čtení dostupného HA v WSLc 3.0.5.0 vrátilo HTTP 200. Preflight z jeho síťového prostoru proti kontrolní adrese v kamerové podsíti vybral zdroj mimo tuto podsíť. Stejný výsledek dal aktuální Windows shell. Měření neodeslalo CS2 datagramy a neověřovalo dostupnost jednotlivé kamery. Fyzický test vyžaduje adresu testovacího hostitele se správnou síťovou cestou.
- **OPEN** – autorizovaný přímý přenos a dekódování C201 v HA. Funkční přihlášení z existujícího testu gateway se nepovažuje za ověřený tok nové integrace.

### Pokračování pouze s WSLc

- **BLOCKED (fyzická síť)** – WSLc 3.0.5.0 vypisuje předdefinovanou síť `host`, ale jednorázový `wslc run --network host` skončil ještě před startem image s hlášením „Síť v hostitelském režimu není podporována“. Tento pokus neověřoval nastavení mirrored network v běžné distribuci WSL. Stávající HA zůstal v provozu.
- **PASS (framing)** – Python prototyp přečte video metadata MISS a Annex B pro H.264/H.265 z již dešifrovaného rámce. Rozlišuje konfigurační NAL jednotky a snímek, od nějž lze zahájit dekódování. Devět offline testů proběhlo na Windows a v HA image `2026.10.4` s vypnutou sítí.
- **PASS (syntetické dekódování)** – v témže HA image vytvořil FFmpeg syntetický H.265 snímek 64×64. Po přidání syntetické MISS hlavičky a průchodu Python parserem FFmpeg dekódoval jeden celý YUV snímek. Test nepotřebuje účet, gateway ani kameru.
- **OPEN (přímý backend)** – CS2 spojení, klíče a dešifrování MISS, otevření streamu z integrace a živý obraz C201. WSLc může ověřovat simulovanou relaci a mediální adaptér. Přejímka kamery zůstává oddělená.

### Pokus v Ubuntu WSL bez WSLc

- **PASS (offline)** – všech devět rychlých testů prošlo přímo v Ubuntu 24.04 pod WSL 2. V distribuci je Python 3, FFmpeg není v PATH. Stávající HA ve WSLc dál odpovídal HTTP 200.
- **BLOCKED (fyzická síť)** – `wslinfo --networking-mode` vrátilo `nat`. Kontrola trasy z distribuce bez vysílání datagramů vrátila `source_outside_camera_subnet` (exit 2).
- Další síťový pokus na této stanici: nastavit `networkingMode=mirrored` v globálním `.wslconfig`, obnovit WSL a zkontrolovat režim i trasu před pokusem s fyzickou C201. Restart WSL přeruší běžící testovací HA a může ovlivnit další WSL procesy. Konfigurace i běžící služby v tomto kroku zůstaly beze změny. [Síťové režimy WSL](https://learn.microsoft.com/en-us/windows/wsl/networking#mirrored-mode-networking) · [Konfigurace WSL](https://learn.microsoft.com/en-us/windows/wsl/wsl-config)

### Ověření nativních Windows

- **Ověřeno** – Python `3.15.2` existuje na uživatelem dodané cestě a funguje. Instalace Home Assistant Core `2026.10.4` z binárních balíčků na 3.15 narazila na chybějící Windows wheel `annotatedyaml==1.0.2`.
- **Výchozí omezení Core** – na přenosném Pythonu `3.14.2` se Core `2026.10.4` nainstaloval s oficiálními omezeními a se sestavením `pyric` ze zdrojů. Neupravený start i `check_config` skončily s exit 1 a hlášením `Home Assistant only supports Linux, OSX and Windows using WSL`. Oficiální instalace pro Windows používá HA OS ve virtuálním stroji. Příkazy Hyper-V jsou dostupné, ale současný účet nemá oprávnění ani pro výpis VM a virtuálních přepínačů. [Požadavky balíčku](https://pypi.org/project/homeassistant/2026.10.4/) · [Instalace HA ve Windows](https://www.home-assistant.io/installation/windows/)
- **PASS (nativní testovací HA)** – místní kopie Core povoluje Windows, drží zámek konfigurace přes `msvcrt`, vynechává nastavení unixového limitu deskriptorů a registruje `SIGINT`/`SIGTERM` přes Windows kompatibilní obsluhu. Původní soubory jsou vedle upravených jako `.upstream`. Běží na [127.0.0.1:56264](http://127.0.0.1:56264/). Kontrola konfigurace prošla, úvodní HTML odpovědělo HTTP 200 a WebSocket vrátil `auth_required` s verzí `2026.10.4`. FFmpeg používá stávající lokální Windows binárku. Přihlášení a fyzická kamera v této instanci zůstávají otevřené. Původní HA ve WSLc dál odpovídá HTTP 200.
- Startovací skript nativní instance v této pracovní kopii je `.test-env/ha-native-win-py314/start-native-ha.ps1`. Instalace je izolovaná pod `.test-env`. Aktualizace Home Assistant může lokální úpravy přepsat.
- **BLOCKED (fyzická síť)** – preflight z Windows na kontrolní adresu v dosud používané kamerové podsíti také skončil `source_outside_camera_subnet`. Windows při kontrole neměly adresu z této podsítě. Záznam neurčuje dostupnost konkrétní kamery ani stav routování mezi VLAN. Pro přejímku ověřit aktuální kamerovou podsíť a připojení testovacího stroje.

### Zrcadlená síť WSL a další offline krok

- **BLOCKED (fyzická síť)** – dočasná změna `.wslconfig` skutečně přepnula Ubuntu do režimu `mirrored`, potvrzeného příkazem `wslinfo --networking-mode`. Preflight na kontrolní adrese ve stejné historické podsíti opět skončil `source_outside_camera_subnet` (exit 2). Sonda neodeslala datagram ke kameře. Po pokusu byla obnovena původní konfigurace NAT, všech pět existujících WSLc kontejnerů hlásilo `running` a testovací HA odpověděl HTTP 200.
- **PASS (MISS video)** – Python dešifruje nonce a šifrovanou část mediální zprávy z již sjednaného klíče. Výstup odpovídá syntetickému ChaCha20 vektoru vytvořenému Go referencí. Prošlo 11 rychlých testů na Windows Python 3.14 a v HA image `2026.10.4`. V izolovaném Linuxovém HA image bez sítě navíc FFmpeg dekódoval celý syntetický H.265 snímek prošlý šifrováním, dešifrováním a parserem.
- **Přímý klient C201** – klient umí požádat přihlášený účet o podepsaný dočasný klíč, vyjednat CS2/TCP, přihlásit se ke kameře a otevřít video bez Go gateway. Simulátor ověřuje změnu portu během handshake, přihlášení, šifrovaný start videa, složení rozděleného snímku a dvě otevření po sobě. Zatím je dostupný jen TCP transport.

### Přímé kamery v Home Assistantu

Zkopíruj celý adresář `custom_components/xiaomi_direct_pilot` do stejnojmenného adresáře v konfiguraci HA a restartuj HA. V **Nastavení → Zařízení a služby → Přidat integraci** najdi **Xiaomi Camera Direct** a vyber region. S výchozí volbou **Xiaomi Home QR** schval účet pomocí skeneru účtu Xiaomi v telefonu a odešli QR formulář v HA. Průvodce přidá všechny nalezené kamery. Výběr lze změnit přes **Konfigurovat**. Po odmítnutí uložené autorizace nabídne HA přihlášení ke stejnému účtu. Pro adresu používá místní záznam odpovídající MAC z inventáře, případně IPv4 adresu, kterou poskytlo Xiaomi. Zdrojovou adresu vybírá podle skutečné trasy ke kameře. Entita bez podporovaného modelu nebo místní adresy se zobrazí jako nedostupná. Průvodce ukládá autorizaci pro další spojení. Heslo se neukládá.

Pro nové nastavení použijte průvodce výše; nevyžaduje ruční kopírování tokenů do YAML. Jen při obnově již existující uložené autorizace lze místo průvodce přidat položku do sekce `camera:`:

```yaml
camera:
  - platform: xiaomi_direct_pilot
    name: Xiaomi C201 Direct
    device_id: !secret xiaomi_direct_c201_id
    camera_ip: 192.168.99.2
    source_ip: 192.168.99.3
    camera_lan: 192.168.99.0/24
    region: de
    account_user_id: !secret xiaomi_direct_user_id
    account_pass_token: !secret xiaomi_direct_pass_token
```

Nahraď ukázkové adresy adresou C201, lokální IPv4 adresou HA a jejich společnou podsítí. Hodnoty `!secret` patří do místního `secrets.yaml`. Po konfiguraci otevři novou entitu kamery s názvem končícím na **Direct**. Průvodce zatím nepodporuje CAPTCHA. Přechod z Go gateway je vedený v etapě D2.

HA otevře kameru až při požadavku na obraz. Video zůstává na LAN a HA z něj vytvoří HLS a snímek. C201, C400, Mi 360 a C701 mají v přímé integraci čtyři krokové směry PTZ. Ovládání používá právě otevřenou relaci a nevytváří samostatné připojení ke kameře. Pevná AW300 šipky nemá.

- **PASS (simulovaný HA stream)** – test `tests/e2e/test_ha_direct_ha_stream.py` používá simulovaný C201, vytvoří položku přes průvodce bez hesla, dekóduje HLS snímek 64 × 64, získá JPEG a kontroluje ukončení relace. Tento výsledek nedokládá připojení k fyzické kameře.
- **OPEN (cílené automatické testy)** – test účtu i test `tests/e2e/test_ha_direct_ha_stream.py` byly zkuseny v připraveném nativním Python 3.14 prostředí. Oba běhy uvázly při vytváření Windows asyncio event loop (traceback ukázal blokující socket `accept` při tvorbě self-pipe); žádný z nich proto nemá doložený výsledek PASS. V systémovém Pythonu testy nelze importovat kvůli chybějícím závislostem HA/aiohttp.
- **PASS (fyzická C201 v testovacím HA na Windows)** – po schválení účtu dekódoval FFmpeg z přímé entity 98 snímků za pět sekund videa. První snímek samostatné zkoušky se čtyřmi entitami přišel za 6,9 s.
- **PASS (inventář fyzického účtu)** – HA načetl čtyři kamery a pro každou vybral adresu na LAN. Všechny jsou na panelu **Xiaomi přímo**. Při prvním společném testu C400, Mi 360 a AW300 nedoručily dekódovaný snímek do 45 s. Samostatná sonda u C400 a AW300 obešla HLS a došla k čekání na první médium, u Mi 360 vypršel čas navázání CS2.
- **Rozběh a opakované otevření** – integrace sdílí autorizovanou HTTP relaci účtu, zkracuje analýzu lokálního MPEG-TS a při ukončení posílá příkaz k zastavení videa. C400 a AW300 mají nejvýše jeden rychlý pokus navíc před předáním chyby HA. V nativním HA na Windows se v prohlížeči přehrávaly C201 a C400, C400 při šířce 2560 px. Při opětovném otevření už rozehraného panelu se obraz zobrazil za 1,2 s a 2,4 s. Časy neplatí jako měření startu relace od nuly. Při samostatné přímé sondě bez přehrávače HA přišlo první médium C201 za 1,21 s a C400 za 4,85 s. AW300 po navázání relace neposlala video ve stejném měření ani přes Go referenci. Mi 360 neodpověděla na navázání CS2 ani na místní vyhledání podle MAC.
- **C201 v prohlížeči** – při hlášeném rozpadu obrazu trasa C201 nově převádí video na H.264, drží rozlišení z prvního platného snímku a používá rostoucí časové značky. Cílený offline test podstrčí změnu velikosti snímku a návrat časové značky. Na skutečné C201 FFmpeg přes HA dekódoval 402 snímků za 20 s v rozlišení 848 × 480 bez chyb dekódování a bez návratu časových značek při milisekundové časové základně. V prohlížeči postupovala časová osa videa C201. Dlouhodobý běh a opakovatelnost závady nejsou ověřeny.
- **Přímé PTZ a AW300** – panel **Xiaomi přímo** zobrazuje čtyři šipky v obrazu kamer s PTZ. Izolovaný HA test předal jeden povel a test drátu ověřil šifrovaný MISS příkaz. Nejnovější potvrzení uživatele uvádí fyzický pohyb jako funkční na C201, C400 a C701; tím se přepisuje tehdejší poznámka níže, že fyzický pohyb nebyl nezávisle měřen. AW300 je pevná kamera. Její živý stream zůstává otevřený; starší sondy navázaly relaci, ale nedodaly první médium.
- **Panel WebRTC Camera** – testovací HA používá [AlexxIT WebRTC Camera v3.6.1](https://github.com/AlexxIT/WebRTC/releases/tag/v3.6.1) s již běžícím lokálním go2rtc. Čtyři karty používají přímé entity pomocí `entity`, režimy `webrtc,mse` a šipky přes `shortcuts` s akcí `button.press`. Sdílení zdroje odstranilo HTTP 409 při více odběratelích. Test ověřil, že dva HTTP přehrávače dostanou data z jediné relace a že se relace po odpojení uvolní. V prohlížeči běžela fyzická C201 přes RTC a C400 přes MSE. U AW300 a Mi 360 zůstává problém na straně přímého zdroje.
- **Opakované ověření AW300 (historický běh)** – uživatel tehdy potvrdil, že AW300 běží a Mi 360 je fyzicky odpojená. Testovací HA přehrával C201 a C400, AW300 vrátila HTTP 503; sonda AW300 nedostala první médium. Tento výsledek byl nahrazen novějším uživatelským potvrzením streamu C201/C400/C701/Mi 360. AW300 zůstává otevřená k pozdějšímu ověření.
- **AW300 s vynuceným CS2/UDP** – při kvalitě `0` a vypnutém mikrofonu se dvakrát podařila autorizace a odeslání příkazu ke startu s potvrzením na úrovni CS2. Oba běhy skončily bez jediného videopaketu. Druhý běh sledoval aplikační odpověď MISS a ukončení relace. Během čekání na video nedostal odpověď MISS na start ani paket pro ukončení relace. Souhrnný záznam ukončovací paket obsahoval až mimo sledované okno. [Hlášení stejného modelu v go2rtc](https://github.com/AlexxIT/go2rtc/issues/2291)
- **AW300 s prodlevou po autorizaci** – cílená zkouška přidala sekundu mezi úspěšné přihlášení a příkaz ke spuštění, aby ověřila rozdíl v časování Go a Python klienta. Nedostala ani potvrzení příkazu na úrovni CS2 a přijala 0 videopaketů. Prodleva se do integrace nepřidala.
- **Xiaomi Camera Direct 0.2.0 v nativním HA 2026.10.4** – aktualizace zachovala ID položky i původní autorizaci. Živý registr HA obsahuje čtyři zařízení, čtyři původní entity kamer a 12 tlačítek přiřazených ke kamerám. Přímo z entity C201 uložil HA JPEG (51 678 bajtů) a MP4 (21,99 s). FFmpeg dekódoval první dvě sekundy MP4 s návratovým kódem 0. Z přímé entity C400 dekódoval 99 snímků za 5 s videa, celý pokus trval 12 s. Ve zkušebním HA se simulátorem prošla obnova přihlášení pro stejný účet, odmítnutí jiného účtu a změna výběru kamer se zachováním ID. Prošlo 27 rychlých testů a pět mediálních a HA E2E testů. Po simulovaném pádu spojení dostali dva aktivní odběratelé klíčový snímek z jedné nové relace s rostoucí časovou značkou. Obnova po fyzickém výpadku zbývá ověřit.
- **Kontrola finálního nasazení 0.2.0** – po restartu testovacího HA prošel přímý HLS obou fyzických kamer. FFmpeg dekódoval z C201 104 snímků a z C400 99 snímků, vždy za pět sekund videa. Celé zkoušky trvaly 7 s a 23 s včetně dekódování. Během startu C400 zaznamenal HA jednu chybu demuxu. Jednotlivý úspěšný výsledek nedokládá splnění cíle pro studené starty.

### Volba rozlišení C400 (F03)

Na fyzické `chuangmi.camera.039a04` v regionu DE se dvěma krátkými přímými relacemi po LAN ověřily tyto profily:

| Profil MISS | Dekódovaný obraz | Snímků/s | Tok video payloadu |
| --- | --- | --- | --- |
| `2` | H.265, 864 × 480 | 19,9 | 330 kbit/s |
| `3` | H.265, 2560 × 1440 | 19,5 | 3140 kbit/s |

Hodnoty toku pocházejí z 2,5 s záznamu metadat v konkrétní scéně. **Konfigurovat** nabízí od verze 0.2.1 výběr C400 pro nižší rozlišení. Neoznačené C400 používají výchozí profil `3`. Hodnota `requested_video_profile` v entitě uvádí profil vyžádaný od kamery. Uložení nastavení znovu načte účet a další přehrávání otevře relaci se zvoleným profilem. Regrese ověřuje odmítnutí jiné kamery, přechod z `3` na `2` a zpět, nulové otevření bez odběratele a bajty příkazu MISS u obou hodnot.

**PASS (fyzická C400, nativní HA 2026.10.4).** Volba v českém formuláři přepnula živou kartu na 864 × 480 a čas přehrávání postupoval. Po uložení výchozího profilu karta krátce ukazovala předchozí rozlišení. Po novém otevření panelu přehrávala 2560 × 1440. Výchozí profil byl po přejímce ponechán. Prošlo 27 rychlých a pět mediálních a HA E2E testů.

### Zvuk z C201 (F02)

- **Implementováno (0.2.2)** – volba **Mikrofon C201 (experimentální)** v regionu DE je standardně vypnutá. Zapnutí požádá o zvuk pouze při otevření relace. G.711 A-law nebo μ-law z téže relace se pro nativní HLS převádí na AAC mono 16 kHz. Pokud se při startu nepodaří rozpoznat podporovaný zvuk, přehrává se obraz. Při zapnutí může být zvuk součástí záznamu HA.
- **PASS (simulovaná kamera)** – test kontroluje příznak mikrofonu na drátu, zahození poškozeného zvukového paketu, obraz i při chybějícím zvuku, výchozí HLS bez audio stopy a dekódování AAC do 6 400 bajtů PCM přes nativní HLS.
- **FAIL (fyzická C201, metadatová sonda)** – jediný schválený pokus do 10 s skončil CS2 chybou. Formát mikrofonu se nepotvrdil. Zvuk se nepřehrával ani neukládal.
- **PASS (nasazení na nativní testovací HA 2026.10.4)** – český formulář verze 0.2.2 zobrazil vypnutou volbu mikrofonu. Po aktualizaci se na panelu přehrávala C201 při 848 × 480 a C400 při 2560 × 1440. Prošlo 28 rychlých a pět mediálních a HA E2E testů.
- **PASS (fyzická C201, verze 0.2.3)** – přímá sonda s profilem `2` přes CS2/TCP napočítala 97 Opus paketů s typickým intervalem 40 ms a 73 videopaketů H.265. Po převodu stejnou mediální cestou jako v integraci se na testovací stanici přehrálo 32 000 vzorků při 16 kHz. Audiopřehrávač skončil s návratovým kódem 0 a zvuk zůstal v paměti. Zapnutí mikrofonu v HA nyní volí toto ověřené nastavení. Výchozí přenos videa s vypnutým mikrofonem dál používá automatický profil.

Pro stejnou kartu `custom:webrtc-camera` lze zachovat výchozí rychlé zobrazení a přidat přepnutí na přehrávání AAC přes MSE. Do konfigurace karty C201 přidej `streams`, přitom ponech existující `shortcuts` a `muted: true`:

```yaml
streams:
  - name: Živě
    entity: camera.xiaomi_c201_direct
    mode: webrtc,mse
    media: video
  - name: Se zvukem
    entity: camera.xiaomi_c201_direct
    mode: mse
    media: video,audio
```

Po povolení mikrofonu a dokončení nového načtení integrace přepni kartu na **Se zvukem**. Režim lze změnit i kliknutím na indikátor **RTC/MSE**. Ikona hlasitosti se objeví, když přehrávač rozpozná zvukovou stopu.

**Přejímka panelu zůstává otevřená.** C201 na testovacím panelu přešla mezi RTC a MSE, ale během krátkých zkoušek se zvuk v prohlížeči nepotvrdil. Ověřené přehrání proběhlo přes přímou mediální vrstvu na testovací stanici. Mikrofon zůstal po zkouškách vypnutý a předání panelu ověřilo živý obraz C201 i C400. Po doplnění Opus a volby profilu prošlo 28 rychlých a pět integračních testů.

### Výstup do reproduktoru C201 (F11)

Testovací sonda připravila nízkoúrovňový 0,8s syntetický tón Opus pro reproduktor C201 a mířila na jednu fyzickou kameru. Při běhu s hostitelskou sítí přerušila relace spojení při zkoušce reproduktoru dřív, než se odeslal první zvukový paket. Po uvolnění živého náhledu vypršel čas již při otevření CS2/TCP. V obou bězích s hostitelskou sítí byl počet odeslaných zvukových paketů 0. Mikrofon se nežádal. Výstup do reproduktoru čeká na fyzickou přejímku. [Referenční přenos reproduktoru v go2rtc](https://github.com/AlexxIT/go2rtc/blob/master/pkg/xiaomi/miss/backchannel.go)

### Živá referenční sestava na nativních Windows

- **Odezva PTZ** – integrace pošle povel rovnou na aktivní relaci. Pouze po explicitní odpovědi `ptz_session_unavailable` připraví zdroj a pošle jeden krok znovu. Windows převodník mapuje tuto odpověď ze starší běžící gateway. Cílený požadavek HA pro pohyb C201 vlevo dosáhl zápisu povelu za 11 ms, kamera odpověděla za dalších 207 ms a HA vrátil HTTP 200 za 228 ms. Fyzický začátek pohybu ani jeho zpoždění v HLS nebyly měřeny.
- **PASS (HA a inventář)** – nativní HA zpřístupňuje společnou [kartu čtyř kamer](http://127.0.0.1:56264/xiaomi-live/mi-360). Po obnovení integrace byly všechny čtyři entity dostupné, včetně 12 tlačítek PTZ na C201, Mi 360 a C400. Fyzický pohyb se při této přejímce nespouštěl. Skripty snímku a záznamu ukládají do místní testovací složky. Uložená konfigurace HTTP potvrdila loopback port `56264`, takže HA po pěti minutách automaticky nerestartuje na jiný port.
- **PASS (Mi 360, C201, C400)** – testovací Windows sestava používá existující Go gateway a Windows go2rtc 1.9.15. Po obnově zdroje nedrží dočasnou RTSP cestu jako adresu pro další spojení. go2rtc při startu každého zdroje načte čerstvou cestu z gateway a HA používá stálou místní adresu. Ověření levným testovacím agentem dekódovalo na každé z těchto tří kamer 50 snímků za pět sekund videa. V tomto běhu C400 potřebovala 56 sekund včetně čekání na přehratelný výstup.
- **FAIL (AW300)** – kamera byla v inventáři a navázala CS2, ale nedodala první snímek při Windows testech s vyjednaným TCP ani UDP. Proto pro ni přehrávač nedostal zdroj. Další pokus porovnat ve stejnou dobu s náhledem v Xiaomi Home.
- **PASS (lokální snímek a záznam C201)** – oba skripty HA vytvořily soubory ve Windows. JPEG měl 86 845 bajtů a platné značky začátku a konce. MP4 měl 22,50 s a FFmpeg jej dekódoval s návratovým kódem 0.
- Startovací skript je `.test-env/ha-native-win-py314/start-native-camera-stack.ps1`. Očekává aktivní testovací gateway na portu `49205`. Testovací účet je uložen pouze v místním souboru `.test-env/ha-native-win-py314/login.json`. Tato sestava je výchozí srovnání pro nativní Windows; přímý Xiaomi backend bez gateway ani živé Frigate tím ověřené nejsou.
- **Doba do prvního snímku (Windows)** – místní převod FFmpeg analyzuje až 0,5 s vstupního videa, zachovává časování snímků a vkládá klíčové snímky po 2 s. HA má nastavenou délku HLS segmentu 2 s a části 0,5 s. Kontrola konfigurace prošla. Sonda `.test-env/ha-native-win-py314/measure-first-frame.py` měří od požadavku přes WebSocket HA do prvního dekódovaného snímku bez vypisování URL. V běžící instalaci naměřila C201 0,24 s, C400 0,34 s a AW300 0,36 s. Časy zahrnují možné využití už otevřených zdrojů, takže nedokládají zrychlení studeného startu. Mi 360 selhala už při otevírání zdroje ve dvou pokusech (9,03 a 10,02 s). Gateway při cíleném požadavku vrátila HTTP 503 s kódem `stream_timeout`.
