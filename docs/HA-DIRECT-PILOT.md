# Přímá integrace HA: D0 a začátek D1

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
- **OPEN** – navázání transportu CS2, získání a sjednání klíče, příjem skutečného streamu v HA a fyzická přejímka C201. Příští offline zkouška přidá kontrolovanou CS2 relaci před ověřenou mediální vrstvu.

### Živá referenční sestava na nativních Windows

- **Odezva PTZ** – integrace pošle povel rovnou na aktivní relaci. Pouze po explicitní odpovědi `ptz_session_unavailable` připraví zdroj a pošle jeden krok znovu. Windows převodník mapuje tuto odpověď ze starší běžící gateway. Cílený požadavek HA pro pohyb C201 vlevo dosáhl zápisu povelu za 11 ms, kamera odpověděla za dalších 207 ms a HA vrátil HTTP 200 za 228 ms. Fyzický začátek pohybu ani jeho zpoždění v HLS nebyly měřeny.
- **PASS (HA a inventář)** – nativní HA zpřístupňuje společnou [kartu čtyř kamer](http://127.0.0.1:56264/xiaomi-live/mi-360). Po obnovení integrace byly všechny čtyři entity dostupné, včetně 12 tlačítek PTZ na C201, Mi 360 a C400. Fyzický pohyb se při této přejímce nespouštěl. Skripty snímku a záznamu ukládají do místní testovací složky. Uložená konfigurace HTTP potvrdila loopback port `56264`, takže HA po pěti minutách automaticky nerestartuje na jiný port.
- **PASS (Mi 360, C201, C400)** – testovací Windows sestava používá existující Go gateway a Windows go2rtc 1.9.15. Po obnově zdroje nedrží dočasnou RTSP cestu jako adresu pro další spojení. go2rtc při startu každého zdroje načte čerstvou cestu z gateway a HA používá stálou místní adresu. Ověření levným testovacím agentem dekódovalo na každé z těchto tří kamer 50 snímků za pět sekund videa. V tomto běhu C400 potřebovala 56 sekund včetně čekání na přehratelný výstup.
- **FAIL (AW300)** – kamera byla v inventáři a navázala CS2, ale nedodala první snímek při Windows testech s vyjednaným TCP ani UDP. Proto pro ni přehrávač nedostal zdroj. Další pokus porovnat ve stejnou dobu s náhledem v Xiaomi Home.
- **PASS (lokální snímek a záznam C201)** – oba skripty HA vytvořily soubory ve Windows. JPEG měl 86 845 bajtů a platné značky začátku a konce. MP4 měl 22,50 s a FFmpeg jej dekódoval s návratovým kódem 0.
- Startovací skript je `.test-env/ha-native-win-py314/start-native-camera-stack.ps1`. Očekává aktivní testovací gateway na portu `49205`. Testovací účet je uložen pouze v místním souboru `.test-env/ha-native-win-py314/login.json`. Tato sestava je výchozí srovnání pro nativní Windows; přímý Xiaomi backend bez gateway ani živé Frigate tím ověřené nejsou.
- **Doba do prvního snímku (Windows)** – místní převod FFmpeg analyzuje až 0,5 s vstupního videa, zachovává časování snímků a vkládá klíčové snímky po 2 s. HA má nastavenou délku HLS segmentu 2 s a části 0,5 s. Kontrola konfigurace prošla. Sonda `.test-env/ha-native-win-py314/measure-first-frame.py` měří od požadavku přes WebSocket HA do prvního dekódovaného snímku bez vypisování URL. V běžící instalaci naměřila C201 0,24 s, C400 0,34 s a AW300 0,36 s. Časy zahrnují možné využití už otevřených zdrojů, takže nedokládají zrychlení studeného startu. Mi 360 selhala už při otevírání zdroje ve dvou pokusech (9,03 a 10,02 s). Gateway při cíleném požadavku vrátila HTTP 503 s kódem `stream_timeout`.
