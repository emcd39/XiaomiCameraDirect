# Samostatný pilot Xiaomi C201

## Cíl a výchozí stav

Na fyzické C201 ověřit dekódovaný snímek, živý obraz, obnovu spojení a posun ve čtyřech směrech v samostatném nástroji. První běh bude přímo na notebooku s Windows připojeném k `MoukaHome_IoT`. Výsledný protokolový kód má jít spustit také na Linuxu pro pozdější integraci. Testovací model je `xiaomi.camera.c201`, firmware hlášený jako `5.3.2_0016`, účet EU.

Přihlášený pilot už našel kameru v inventáři. Přímý TCP port 554 odmítl spojení z Windows i z kontejneru. RTSP nabízené bridge skončilo na `DESCRIBE 404` a v jednom časově korelovaném běhu se objevil UDP timeout bez identifikace protistrany. Běžící FFmpeg měl pro lokální RTSP zapnuté TCP. Poslední pokus s trace nepotvrdil přiřazení SDP k odpovědi a původní konfigurace logování byla obnovena. Obraz a fyzické PTZ zůstávají `hardware_pending`. [Dosavadní pokusy](NEXT-TASK.md)

## Referenční implementace a volba nástroje

- [Xiaomi Camera Viewer](https://github.com/lubos-hnanicek/xiaomi-camera-viewer/tree/36d203c6adb8bbef88b53948f39687a1ea8a5c8c), připnutá revize `36d203c6adb8bbef88b53948f39687a1ea8a5c8c`. README popisuje Go bridge pro MISS nad CS2, dekódování H.264/H.265, krokové PTZ a skripty pro zkoušení kvality a ovládání. Doložené modely jsou CW400 a CW500. U nich je relevantní hypotéza UDP discovery na portu 32108 s unicastem i broadcastem a odpovědí z jiného portu. Transport, port, profil i příkazy pro C201 musí potvrdit měření. [README na připnuté revizi](https://github.com/lubos-hnanicek/xiaomi-camera-viewer/blob/36d203c6adb8bbef88b53948f39687a1ea8a5c8c/README.md)
- Pro první pokus použít nejmenší dostupný upstream probe nebo zkontrolovaný referenční viewer. Pro vlastní pilot upřednostnit malý Go CLI se stávající protokolovou implementací a externím FFmpeg/ffplay. Windows DLL, D3D11 a mřížka kamer jsou potřeby referenční aplikace. Dekodér nejdřív spustit softwarově pro jeden stream. Před převzetím kódu zaznamenat konkrétní soubory, jejich původ, verze závislostí a licence. [Licence vieweru](https://github.com/lubos-hnanicek/xiaomi-camera-viewer/blob/36d203c6adb8bbef88b53948f39687a1ea8a5c8c/LICENSE)
- Když C201 prokazatelně používá jiný transport, zachovat důkaz z pokusu a vyzkoušet jeden kandidát, který tento transport skutečně implementuje. Jako výchozí referenci lze použít dříve posouzený vendor backend, pokud poskytne potřebnou diagnostiku mimo HA. Otevřený upstream report o C201 je vodítko k reprodukci. [Kandidáti backendu](ADR-002-MANAGED-BACKEND.md), [go2rtc #2290](https://github.com/AlexxIT/go2rtc/issues/2290)

## Kroky pro levnější model

Každý krok je samostatné zadání. Na konci zapsat `pass`, `fail` nebo `blocked`, poslední potvrzenou fázi, jeden důkaz a nejbližší experiment. Před opakováním pojmenovat měněnou proměnnou. Jeden běh zkouší jednu hypotézu. Při dvou stejných selháních bez nového signálu přeskočit na odpovídající diagnostickou větev. Nejasnosti ohledně transportu, šifrování nebo licence předat s krátkým důkazem místo rozsáhlého přepisu.

| Úkol | Práce | Podmínka uzavření |
| --- | --- | --- |
| S00. Příprava | Zkontrolovat připnutou revizi a najít nejmenší probe pro přihlášení, discovery a video. Ověřit, že notebook je pořád na správné Wi-Fi a má přímou trasu k C201. Uložit verze Windows, nástrojů a probe. V Xiaomi Home udělat krátký kontrolní náhled a pak ukončit živé přehrávání. | Ověřená trasa, známá referenční revize, záznam o stavu obrazu v Xiaomi Home a vybraný první příkaz. |
| S01. Relace na Windows | Přihlášení přes podporovanou cestu Xiaomi EU, výběr jedné C201 podle modelu a lokálního inventáře. Spustit probe přímo ve Windows. Změřit odchozí discovery, odpověď, vyjednaný transport a stav relace. Pokud SDK transport hlásí jako nepodporovaný, zaznamenat jeho název a zastavit tuto větev. | Identifikovaný transport a potvrzená relace, nebo doložená poslední fáze a konkrétní další měření podle tabulky níže. |
| S02. První obraz | Po otevření relace zvolit výchozí nebo jeden nízký profil, nejdřív video bez zvuku. Zaznamenat příchod médií, rozpoznaný kodek, parametry kodeku a první klíčový snímek. Předat obraz FFmpegu nebo ffplay. Další profil zkusit zvlášť pouze tehdy, když relace drží a podezření míří na profil. | Skutečně dekódovaný snímek C201 s rozměry a časem do prvního snímku. Soukromý snímek zůstává lokálně. Samotná HTTP odpověď, SDP, UDP provoz ani rozpoznaný kodek podmínku nesplňují. |
| S03. Samostatný stream | Umožnit jeden příkaz pro snímek a jeden pro živý obraz. Pro ověření stačí lokální přehrávač nebo stream navázaný na loopback. Změřit fps, mezery a dobu do prvního snímku. Přerušit upstream relaci při zachování běžícího klienta a pozorovat přechod do nedostupnosti a vytvoření nové relace. | Alespoň 10 minut měnícího se obrazu, jeden řízený výpadek a automatický návrat čerstvého obrazu. Orientační cíle jsou první snímek do 15 s a obnova do 120 s po návratu sítě. |
| S04. Fyzické PTZ | Zjistit podporované akce pro C201. Krokové MISS PTZ z vieweru použít jako hypotézu až po potvrzení kompatibilního transportu a příkazu. Bez současně otevřeného náhledu Xiaomi Home provést pod dohledem jeden krátký krok v každém směru. U souvislého pohybu ověřit stop včetně ztráty klienta, u kroku samovolné zastavení. Zrušit čekající příkazy při výpadku. | Čtyři potvrzené pohyby a bezpečné zastavení. Důkaz je viditelná změna záběru, pozorovaný pohyb nebo odpovídající měření polohy s vizuálním ověřením. Kladná odpověď API sama nestačí. |
| S05. Další schopnosti | Inventarizovat funkce podle Xiaomi Home a skutečně dostupných schopností. Postupně ověřit poslech, kvalitu obrazu, noční režim, soukromí, detekci a události, pokud jsou dostupné. Testovat jednotlivě a vrátit výchozí stav. Presety, sledování, obousměrný zvuk, microSD a cloudový archiv vést jako samostatné položky. | U každé funkce stav `verified`, `unsupported`, `blocked` nebo `not_tested` a odkaz na důkaz. `unsupported` vyžaduje potvrzení schopností nebo výslovné odmítnutí. Ticho nebo timeout je `blocked`. |
| S06. Přenos na Linux | Po úspěchu ve Windows spustit stejnou revizi protokolového kódu v Linuxu na stejné LAN. Zopakovat snímek, 10 minut videa, obnovu a PTZ. Pokud je potřeba srovnat WSL s NAT, porovnat stejný probe, transport, firmware a profil v obou prostředích. | Reprodukovatelný úspěch na cílovém Linuxu, nebo konkrétní síťový či platformní rozdíl a jeho důkaz. |
| S07. Integrace | Teprve po S01–S04 a ověření cílového prostředí ze S06 přidat adaptér k Xiaomi Local Gateway. Přenést autentizaci, obnovu relace a ověřené schopnosti. Z HA ověřit snímek, živý obraz, obnovu, čtyři směry i akci automatizace. Spustit stávající ONVIF regresi a připravit balení podle plánu dodání. | Fyzická C201 funguje po běžném přidání v testovacím HA. Stávající ONVIF entity a ovládání projdou regresí. Další funkce se vydávají jednotlivě podle výsledků S05. |

## Diagnostika bez dumpu

Jedno spuštění má náhodné `run_id` a jednotný čas UTC. Události mají pole `run_id`, `time_utc`, `elapsed_ms`, `phase`, `result`, `transport` a `peer_role`. Čísla portů a přesné adresy řešit lokálně, do sdíleného výsledku dát role `camera`, `host`, `broadcast`, `relay_candidate`, `loopback` nebo `unknown`. Pro roli `relay_candidate` uložit jen důvod klasifikace. Veřejná adresa sama relay nedokazuje.

Zaznamenávat přechody `account_ready` → `inventory_match` → `discovery_tx` → `discovery_rx` → `transport_selected` → `session_ready` → `first_media` → `first_keyframe` → `decoded_frame`. Snímek a stream měří stejný zdroj. RTSP `OPTIONS`, `DESCRIBE`, SDP a `SETUP` patří do záznamu jen tehdy, když použitá cesta RTSP skutečně nabízí. Rozlišit upstream relaci s kamerou od lokálního RTSP klienta. Například `DESCRIBE 404` na loopbacku určuje výsledek pro klienta, ale samo neurčuje chybu kamery.

Pro PTZ zaznamenat `command_sent`, `ack`, `motion_confirmed` a `motion_stopped`. Pro obnovu `connection_lost`, `new_session` a `fresh_frame`. Zapisovat jen povolená pole a typy chyb. Zpracovat stdout i stderr uvnitř filtru před výpisem. Do sdíleného logu nepatří tokeny, celé URL, identifikátory zařízení, surové zprávy ani obraz. Pro malé modely stačí souhrn a relevantní okno, obvykle do 50 řádků na pokus.

| Poslední zjištění | Další měření |
| --- | --- |
| Discovery neodesláno | Ověřit vybrané rozhraní, lokální bind, IP a masku. Porovnat cílovou adresu a port s kódem skutečně spuštěného probe. |
| Discovery odešlo, odpověď nevidíme | Zachytit krátké okno na Wi-Fi. Pokud zdroj používá CS2, porovnat unicast a directed broadcast. Ověřit v Xiaomi Home, že kamera právě streamuje, a zkontrolovat izolaci klientů na IoT Wi-Fi. Výsledek interpretovat až po ověření, že capture vidí odchozí pakety. |
| Capture vidí odpověď, aplikace ji nedostane | Porovnat cílový port odpovědi s bindem, síťový profil a pravidla firewallu pro konkrétní program. U potvrzeného problému zkusit dočasné úzké pravidlo pro UDP v dané síti a po pokusu ho odstranit. |
| Relace odpoví, vyjednávání selže | Zaznamenat zvolený transport, fázi a typ chyby. Ověřit region, dostupnost klíčů relace a očekávaný protokol podle připnutého kódu. Pokud transport není podporovaný, přejít na kandidáta pro tento transport. |
| Relace běží, média nepřijdou | Zjistit roli protistrany a skutečný mediální socket. Porovnat krátký capture na fyzickém a virtuálním rozhraní pouze v případě podezření na NAT. Měnit vždy jednu věc, například profil nebo transport. |
| Média přijdou, dekodér nemá obraz | Změřit počet a velikosti paketů, kodek, dostupnost parametrů kodeku a prvního klíčového snímku. Přidat offline fixture ze syntetického streamu pro oddělení parseru od sítě. |
| PTZ má ACK bez pohybu | Uzavřít jiné relace s kamerou, ověřit správnou osu a modelový příkaz a udělat jeden kontrolovaný krok. Evidovat ACK a pohyb zvlášť. |

## Síťový dump, pokud logy nestačí

1. **Definovat otázku a místo sběru.** Pro první pokus stačí fyzické Wi-Fi rozhraní notebooku. Při pozdějším podezření na NAT zachytit stejný pokus na fyzickém rozhraní a na virtuálním rozhraní nebo v Linuxovém síťovém prostoru, kudy probe skutečně komunikuje. Pro kontrolní běh Xiaomi Home na telefonu použít capture na vlastním AP či routeru, pokud je k dispozici. Na Wi-Fi rozhraní notebooku se provoz jiného klienta nemusí objevit.
2. **Vymezit provoz.** Začít po přihlášení, 2–3 sekundy před jedním pokusem. Na handshake stačí 20–30 sekund. Filtrovat na kameru a relevantní broadcast nebo na konkrétní protistranu zjištěnou diagnostikou socketu. Filtr jen na kameru a broadcast nezachytí případný cloudový relay. Pro relay vytvořit samostatný úzký filtr podle předem zjištěné protistrany. Ověřit IP a masku před použitím příkladu.
3. **Omezit rozsah.** Krátký capture se zkrácenou délkou paketu může obsahovat část aplikačních dat. PCAP proto zůstává místní v ignorovaném adresáři `.test-env/c201-standalone/private/`. Přihlašování, hesla, tokeny, klíče, zvuk a obraz se nesdílí. Není-li zkrácený záznam dostatečný, rozšířit zachycení jen pro konkrétní protokol, operaci a vlastní zařízení. Plný dump, dešifrování TLS, plošný MITM a přehrávání zachycených příkazů nejsou součástí základního pilotu.
4. **Vyhodnotit a uložit souhrn.** Z PCAP číst pouze čas, rozhraní, směr, protokol, délku, porty a typ odpovědi. Lokální hodnoty přemapovat na role a ven předat jen agregace. Uvést rozhraní, capture filtr, dobu sběru a zda vidíme odchozí požadavek i odpověď. Pro srovnání dvou rozhraní použít stejné časové okno. Nepřítomnost paketu mimo záběr filtru nebo snímacího bodu je neznámý výsledek.

Příklad pro Windows PowerShell po vytvoření soukromého adresáře. Identifikátor rozhraní dosadit z `dumpcap -D`. Adresa broadcastu odpovídá dříve pozorované masce /24.

```powershell
dumpcap -D
$captureInterface = Read-Host 'ID Wi-Fi rozhraní z dumpcap -D'
dumpcap -i $captureInterface -f "host 192.168.3.119 or (udp and dst host 192.168.3.255)" -s 96 -a duration:25 -w .test-env/c201-standalone/private/discovery.pcapng
tshark -n -r .test-env/c201-standalone/private/discovery.pcapng -T fields -e frame.interface_id -e frame.time_relative -e frame.len -e ip.src -e ip.dst -e udp.srcport -e udp.dstport -e tcp.srcport -e tcp.dstport
```

Poslední příkaz je určený k místnímu vyhodnocení. Výstup do chatu či repozitáře vytvořit až po přemapování adres a agregaci. Příznak `-s` omezuje délku paketu a `-a duration` délku zachytávání. [Dumpcap](https://www.wireshark.org/docs/man-pages/dumpcap.html), [TShark](https://www.wireshark.org/docs/man-pages/tshark.html)

## Výstupy a předání

- Samostatný probe má mít reprodukovatelný návod, připnutou verzi, jeden příkaz pro diagnostiku, snímek, stream a bezpečný PTZ krok. Konkrétní názvy příkazů zvolit po zkoušce upstream nástroje.
- Výsledek běhu uvádí revizi, model, firmware, region, prostředí, poslední úspěšnou fázi, stav snímku, délku živého obrazu, dobu obnovy a fyzický výsledek jednotlivých směrů. Rozlišit potvrzený přímý přenos, potvrzený relay a neurčenou trasu.
- Technické zadání pro další běh obsahuje jen ID kroku, jednu hypotézu, potřebné soubory, podmínku úspěchu a bezpečný výřez důkazů. Ukázka pro první běh: **S00 a S01. Na Windows ověř jednu C201 pomocí připnutého vieweru nebo jeho nejmenšího probe. Vrať poslední fázi discovery či relace. Při chybě navrhni jeden capture filtr a rozhraní pro příští pokus.**
- Přechod do HA vyžaduje skutečný snímek, nejméně 10 minut videa, obnovu a ověřený fyzický PTZ. Výsledky mocků mají vlastní záznam. Po přenosu do HA se platnost pro C201 potvrzuje znovu na běžné instalační cestě. [Přejímka integrace](DELIVERY-PLAN.md)

## Průběh S00, 6. 11. 2026

**Stav: částečně splněno; S01 čeká na potvrzení spuštění vieweru a přihlášení uživatelem.** Upstream zdroj je připnutý na `36d203c6adb8bbef88b53948f39687a1ea8a5c8c`. Release `0.7.5` z GitHubu má zveřejněný SHA-256 `f60f88a589ca7843f149c21c172984a4ccf938cdcbec4ae79035ee87df17db9e`; jeho tag míří na commit `19610a4a083affe15ffd55b718a04c118a6c32b1`, tedy jinou revizi než připnutý zdroj. Přibalený diagnostický skript se bajtově shoduje se skriptem v připnutém zdroji. Jeho `-SelfTest` prošel: archiv, redakce, pole, částečné chyby; žádná kamera ani síť se při něm nepoužila. Windows balík nemá Authenticode podpis a nebyl spuštěn.

Windows má pro `192.168.3.0/24` on-link trasu přes `Wi-Fi`; adresa notebooku je `192.168.3.178/24`. Read-only ping `192.168.3.119` skončil `Success`. TCP `192.168.3.119:554` skončil `ConnectionRefused`, což odpovídá předchozímu měření a nevypovídá o proprietary Xiaomi transportu. V běžném sandboxu Windows síťové API vrátilo chybu přístupu; stejný test v povoleném read-only běhu prošel.

V očekávaných umístěních není `%APPDATA%\XiaomiViewer\config.json` ani `.env` v kořeni repozitáře, `C:\DEV` nebo v kořeni profilu. Není tedy připravená uložená relace vieweru. `go`, `cmake`, `dumpcap`, `tshark`, `ffmpeg` a `ffplay` nejsou v PATH; `wsl --list` vrací `E_ACCESSDENIED`. `wslc` hlásí verzi `3.0.3.0`. Diagnostický collector se pro první běh nepoužije v nezměněné podobě: upstream ho záměrně nastavuje s `audio=true`, zatímco S01/S02 má začít pouze videem. Zdroj vieweru podporuje `audio:false` v konfigurační položce kamery; před otevřením relace je třeba tuto volbu bezpečně nastavit.

**Offline S01 příprava:** v `.test-env/c201-standalone/private/collect-c201-video-only.ps1` je lokální kopie collectoru z připnuté revize. Živá větev i self-test používají společnou funkci `New-VideoOnlyOpenRequest`, která nastaví `audio = $false`; redakce odstraňuje URL. Kopie není součástí commitu, SHA-256 je `84472c6b9abbbd2602b9a8d7a479d8399aeea4d8c5fef29ef210a99cc83004a5`. `-SelfTest` vytvořil syntetický archiv a ověřil produkční builder požadavku, odstranění tokenu, device ID, IP i celé RTSP URL a zachování částečné chyby. Výsledek: exit 0. Neotevřel se EXE ani DLL a nebyla použita kamera, účet ani síť.

Po potvrzení spuštění konkrétního balíku musí vlastník přihlásit Xiaomi EU účet ve vieweru, ověřit krátký obraz v Xiaomi Home a náhled ukončit. Až bude viewer session lokálně uložená, spusť z kořene repozitáře jediný S01 běh (bez `-Did` se kamera vybere z lokálního inventáře; zvolit pouze C201):

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\.test-env\c201-standalone\private\collect-c201-video-only.ps1 -DllPath .\.test-env\c201-standalone\private\package\XiaomiViewer-0.7.5-win-x64\xmbridge.dll -Quality auto -ObserveSeconds 8 -OutputDirectory .\.test-env\c201-standalone\private\evidence-s01
```

Tento příkaz je připravený, ale nebyl spuštěn: použije uloženou autorizaci, nahraje přibalenou DLL a otevře jednu video relaci. Záznam odliší cloud transport, relaci, media headers a statistiky. Collector sám snímek nedekóduje; při úspěšných médiích bude dalším krokem S02 s dekodérem. Pokud discovery neodpoví, pořídit 20–30sekundový zkrácený PCAP pouze přes Wi-Fi s filtrem na C201 a příslušný broadcast; PCAP ponechat v `.test-env/c201-standalone/private/` a sdílet jen sanitizované součty a role.
