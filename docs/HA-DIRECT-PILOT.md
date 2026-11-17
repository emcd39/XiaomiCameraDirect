# Xiaomi Camera Direct – záznam testů

## Výchozí stav z 16. 11. 2026

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

HA otevře kameru až při požadavku na obraz. Video zůstává na LAN a HA z něj vytvoří HLS a snímek. C201, C400 a Mi 360 mají v přímé integraci čtyři krokové směry PTZ. Ovládání používá právě otevřenou relaci a nevytváří samostatné připojení ke kameře. Pevná AW300 šipky nemá.

- **PASS (simulovaný HA stream)** – test `tests/e2e/test_ha_direct_ha_stream.py` používá simulovaný C201, vytvoří položku přes průvodce bez hesla, dekóduje HLS snímek 64 × 64, získá JPEG a kontroluje ukončení relace. Tento výsledek nedokládá připojení k fyzické kameře.
- **OPEN (cílené automatické testy)** – test účtu i test `tests/e2e/test_ha_direct_ha_stream.py` byly zkuseny v připraveném nativním Python 3.14 prostředí. Oba běhy uvázly při vytváření Windows asyncio event loop (traceback ukázal blokující socket `accept` při tvorbě self-pipe); žádný z nich proto nemá doložený výsledek PASS. V systémovém Pythonu testy nelze importovat kvůli chybějícím závislostem HA/aiohttp.
- **PASS (fyzická C201 v testovacím HA na Windows)** – po schválení účtu dekódoval FFmpeg z přímé entity 98 snímků za pět sekund videa. První snímek samostatné zkoušky se čtyřmi entitami přišel za 6,9 s.
- **PASS (inventář fyzického účtu)** – HA načetl čtyři kamery a pro každou vybral adresu na LAN. Všechny jsou na panelu **Xiaomi přímo**. Při prvním společném testu C400, Mi 360 a AW300 nedoručily dekódovaný snímek do 45 s. Samostatná sonda u C400 a AW300 obešla HLS a došla k čekání na první médium, u Mi 360 vypršel čas navázání CS2.
- **Rozběh a opakované otevření** – integrace sdílí autorizovanou HTTP relaci účtu, zkracuje analýzu lokálního MPEG-TS a při ukončení posílá příkaz k zastavení videa. C400 a AW300 mají nejvýše jeden rychlý pokus navíc před předáním chyby HA. V nativním HA na Windows se v prohlížeči přehrávaly C201 a C400, C400 při šířce 2560 px. Při opětovném otevření už rozehraného panelu se obraz zobrazil za 1,2 s a 2,4 s. Časy neplatí jako měření startu relace od nuly. Při samostatné přímé sondě bez přehrávače HA přišlo první médium C201 za 1,21 s a C400 za 4,85 s. AW300 po navázání relace neposlala video ve stejném měření ani přes Go referenci. Mi 360 neodpověděla na navázání CS2 ani na místní vyhledání podle MAC.
- **C201 v prohlížeči** – při hlášeném rozpadu obrazu trasa C201 nově převádí video na H.264, drží rozlišení z prvního platného snímku a používá rostoucí časové značky. Cílený offline test podstrčí změnu velikosti snímku a návrat časové značky. Na skutečné C201 FFmpeg přes HA dekódoval 402 snímků za 20 s v rozlišení 848 × 480 bez chyb dekódování a bez návratu časových značek při milisekundové časové základně. V prohlížeči postupovala časová osa videa C201. Dlouhodobý běh a opakovatelnost závady nejsou ověřeny.
- **Přímé PTZ a AW300** – panel **Xiaomi přímo** zobrazuje čtyři šipky v obrazu každé otočné kamery. V izolovaném HA kliknutí předalo právě jeden povel doleva; test drátu ověřil šifrovaný MISS příkaz. Ve fyzickém HA skončil jeden přesně zacílený požadavek na C201 HTTP 200 za 2 ms. Čas potvrzuje přijetí službou HA, fyzický pohyb nebyl nezávisle měřen. AW300 v cílených sondách navázala relaci přes TCP i UDP a po odeslání startu nedodala mediální data při zapnutém ani vypnutém zvuku.
- **Panel WebRTC Camera** – testovací HA používá [AlexxIT WebRTC Camera v3.6.1](https://github.com/AlexxIT/WebRTC/releases/tag/v3.6.1) s již běžícím lokálním go2rtc. Čtyři karty používají přímé entity pomocí `entity`, režimy `webrtc,mse` a šipky přes `shortcuts` s akcí `button.press`. Sdílení zdroje odstranilo HTTP 409 při více odběratelích. Test ověřil, že dva HTTP přehrávače dostanou data z jediné relace a že se relace po odpojení uvolní. V prohlížeči běžela fyzická C201 přes RTC a C400 přes MSE. U AW300 a Mi 360 zůstává problém na straně přímého zdroje.
- **Opakované ověření AW300** – uživatel potvrdil, že AW300 běží a Mi 360 je fyzicky odpojená. V testovacím HA se při kontrole přehrávaly C201 a C400, karta AW300 vrátila HTTP 503. Sonda mimo HA a go2rtc navázala relaci AW300 s kvalitou `3` i s automatickou kvalitou `0`. U obou po příkazu ke spuštění uplynulo 13 s bez prvních mediálních dat. Mi 360 se při této sondě netestovala.
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

### Živá referenční sestava na nativních Windows

- **Odezva PTZ** – integrace pošle povel rovnou na aktivní relaci. Pouze po explicitní odpovědi `ptz_session_unavailable` připraví zdroj a pošle jeden krok znovu. Windows převodník mapuje tuto odpověď ze starší běžící gateway. Cílený požadavek HA pro pohyb C201 vlevo dosáhl zápisu povelu za 11 ms, kamera odpověděla za dalších 207 ms a HA vrátil HTTP 200 za 228 ms. Fyzický začátek pohybu ani jeho zpoždění v HLS nebyly měřeny.
- **PASS (HA a inventář)** – nativní HA zpřístupňuje společnou [kartu čtyř kamer](http://127.0.0.1:56264/xiaomi-live/mi-360). Po obnovení integrace byly všechny čtyři entity dostupné, včetně 12 tlačítek PTZ na C201, Mi 360 a C400. Fyzický pohyb se při této přejímce nespouštěl. Skripty snímku a záznamu ukládají do místní testovací složky. Uložená konfigurace HTTP potvrdila loopback port `56264`, takže HA po pěti minutách automaticky nerestartuje na jiný port.
- **PASS (Mi 360, C201, C400)** – testovací Windows sestava používá existující Go gateway a Windows go2rtc 1.9.15. Po obnově zdroje nedrží dočasnou RTSP cestu jako adresu pro další spojení. go2rtc při startu každého zdroje načte čerstvou cestu z gateway a HA používá stálou místní adresu. Ověření levným testovacím agentem dekódovalo na každé z těchto tří kamer 50 snímků za pět sekund videa. V tomto běhu C400 potřebovala 56 sekund včetně čekání na přehratelný výstup.
- **FAIL (AW300)** – kamera byla v inventáři a navázala CS2, ale nedodala první snímek při Windows testech s vyjednaným TCP ani UDP. Proto pro ni přehrávač nedostal zdroj. Další pokus porovnat ve stejnou dobu s náhledem v Xiaomi Home.
- **PASS (lokální snímek a záznam C201)** – oba skripty HA vytvořily soubory ve Windows. JPEG měl 86 845 bajtů a platné značky začátku a konce. MP4 měl 22,50 s a FFmpeg jej dekódoval s návratovým kódem 0.
- Startovací skript je `.test-env/ha-native-win-py314/start-native-camera-stack.ps1`. Očekává aktivní testovací gateway na portu `49205`. Testovací účet je uložen pouze v místním souboru `.test-env/ha-native-win-py314/login.json`. Tato sestava je výchozí srovnání pro nativní Windows; přímý Xiaomi backend bez gateway ani živé Frigate tím ověřené nejsou.
- **Doba do prvního snímku (Windows)** – místní převod FFmpeg analyzuje až 0,5 s vstupního videa, zachovává časování snímků a vkládá klíčové snímky po 2 s. HA má nastavenou délku HLS segmentu 2 s a části 0,5 s. Kontrola konfigurace prošla. Sonda `.test-env/ha-native-win-py314/measure-first-frame.py` měří od požadavku přes WebSocket HA do prvního dekódovaného snímku bez vypisování URL. V běžící instalaci naměřila C201 0,24 s, C400 0,34 s a AW300 0,36 s. Časy zahrnují možné využití už otevřených zdrojů, takže nedokládají zrychlení studeného startu. Mi 360 selhala už při otevírání zdroje ve dvou pokusech (9,03 a 10,02 s). Gateway při cíleném požadavku vrátila HTTP 503 s kódem `stream_timeout`.
