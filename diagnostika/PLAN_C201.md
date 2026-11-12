# Xiaomi Smart Camera C201 — diagnostika a praktické zprovoznění

**Verze:** 1.0 | **Datum:** 2026-11-07 | **Rozsah:** C201 (`xiaomi.camera.c201`)

## 0. Účel a pravidla

Cíl je **nejprve rozchodit obraz, audio a PTZ pomocí existujících nástrojů**, teprve poté navrhovat integraci do Home Assistantu a Frigate. Preferujeme zachovat originální firmware. V této fázi neprovádíme root, flash, reset, úpravy aplikace Xiaomi, náhodné MIoT zápisy, neznámé PTZ příkazy ani automatické aktualizace firmwaru.

Nástroje: telefon s funkční Xiaomi Home, Windows 10/11 PC s GPU s podporou D3D11, Xiaomi Camera Viewer, go2rtc (Windows portable nebo později samostatný Docker), volitelně Nmap, Wireshark, FFmpeg/ffprobe. Většina testů je zdarma. V PC a kameře použijte **stejnou lokální podsíť / broadcast segment**, nejlépe přímo stejný Wi-Fi/LAN bez VPN, oddělené guest Wi-Fi a AP isolation. Xiaomi Camera Viewer výslovně upozorňuje na potíže s jinými podsítěmi. Windows Firewall musí povolit příchozí UDP pro Viewer; nikdy kvůli tomu plošně nevypínejte firewall.

Pro každý pokus zaznamenejte datum/čas (včetně časové zóny), změněný parametr a výsledek. Testujte vždy jen **jednu proměnnou**.

### Úrovně úspěchu

| Úroveň | Definice | Akceptace |
|---|---|---|
| L0 | Kamera funguje v Xiaomi Home | obraz, PTZ, mikrofon dle nabídky aplikace |
| L1 | Model, síť a protokol jsou popsané | vyplněný baseline + diagnostické soubory |
| L2 | Obraz jde do nezávislého LAN klienta | min. 5 min stabilního videa, 3× nové připojení |
| L3 | Obraz, zvuk a PTZ | funkční dvě osy PTZ, rozlišení/kodek změřené, audio podle dostupnosti |
| L4 | Nová relace vznikne bez Xiaomi Cloudu | blokace WAN před navázáním, restart klienta, restart kamery |
| L5 | Provoz spolehlivý pro další integraci | min. 24 h běhu + test zotavení po výpadku |

L2 je první vítězství; L4 může být zatím nedostupné, protože go2rtc uvádí vyjednávání klíčů přes internet pro každé nové spojení.

## Aktuální stav diagnostiky (2026-11-07)

### Potvrzené údaje a pokusy

- Účet Xiaomi EU inventarizuje model `xiaomi.camera.c201`. Zaznamenaný firmware je `5.3.2_0016`, plugin kamery `4560.8.1-8`; HW revize, verze Xiaomi Home a OS telefonu zůstávají neznámé. Identifikátory účtu a zařízení zůstávají pouze v místním přihlášení.
- Soukromý diagnostický archiv Xiaomi Camera Viewer 0.7.5 (`.test-env/c201-standalone/private/evidence-s01/XiaomiViewer-diagnostics-20261106-165552-37a5d3ae.zip`) uvádí model C201, region `de` (EU), 21 úspěšných kroků a žádný neúspěšný krok. Vyjednání transportu hlásilo vendor CS2; otevřená relace byla `cs2+tcp`, `quality=auto`, výchozí kanál, `audio=false`.
- Viewer collector hlásil 204 media frames a 155 929 bajtů, 114 dropped a 195 vzorků hlaviček. Všech 195 vzorků má codec ID 5 (v aktuálním protokolu H.265) a velikostní pole 29–40 131 bajtů. Archiv neobsahuje media payload ani dekódovaný snímek; rozlišení proto není potvrzené. `dropped` je statistika collectoru, nikoli důkaz ztrát Wi-Fi.
- Dřívější Go pokusy 2026-11-07 obnovily účet a našly jednu C201. Profily `auto`/0 a 1–5 přes automatický transport tehdy skončily na `cs2+tcp` EOF před prvním médiem; profil 2 vynucený přes CS2/UDP otevřel relaci, ale během 10 s nepřišly bajty, hlavičky ani odpovědi. Tyto výsledky jsou historické; novější běh s výchozím profilem už médium přijal. Audio bylo vypnuté ve všech Go pokusech.
- Nový aktuální Go běh 2026-11-07 úspěšně otevřel CS2/TCP, přijal H.265 a připravil RTSP SDP. FFmpeg byl nejdřív volán s nepodporovanou volbou `rw_timeout`; po opravě na RTSP `timeout` dekódování proběhlo.
- L2 technické kritérium je splněno: FFmpeg dekódoval 5 minut (`00:05:00`, 5 937 snímků), kodek HEVC/H.265, 848×480; tři samostatná nová RTSP připojení každé dekódovala 5 snímků a celý každý krátký pokus trval přibližně 3 s. Výstup mířil do `null`; žádný obraz ani zvuk nebyl uložen nebo přehrán. Čas od navázání spojení k prvnímu snímku nebyl samostatně změřen.
- Na začátku souvislého běhu bylo 53 chyb chybějících referencí HEVC. Tři reconnecty měly 22–23 stejných chyb před dokončením pěti snímků. Dostupný log neurčuje jejich příčinu; čekání na keyframe po připojení uprostřed GOP je pouze hypotéza. Každý reconnect přesto dekódoval snímky.
- V Go běhu byl jednou odeslán autentizovaný krátký krok doleva; zápis příkazu uspěl, ale pohyb ani poloha se nepotvrdily. Xiaomi Home PTZ, zvuk a funkce SD nejsou v owner reportu vyplněné. Další PTZ příkazy zatím neposílat.
- Síťová sonda 2026-11-07 09:07 CEST z notebooku na Wi-Fi dostala dvě ICMP odpovědi (14 a 6 ms) a ARP souseda ve stavu `Reachable`. TCP 80, 443, 554, 8554, 8080, 8000 a 54321 skončilo `TIMEOUT/FILTERED`. Dřívější samostatný pokus na TCP 554 skončil `ConnectionRefused`; výsledky portů proto berte jako neprůkazné, nikoli jako důkaz podpory či nepodpory RTSP. MAC není do záznamu opsána. Neupravil se firewall.

### Interpretace a další krok

Go cesta nyní prokazatelně dekóduje H.265 stream přes místní RTSP klienta a splnila L2 čas/reconnect kritérium. Starší EOF se v novém běhu neopakoval. Zdrojový kód ukazuje, že Go nejprve žádá Xiaomi API o CS2 session klíče a potom posílá LAN search/punch na `localip` kamery; to silně podporuje přímou LAN cestu pro tento Go běh, ale nebylo to ověřeno paketovou stopou. U samotného Viewer archivu zůstává direct vs relay nerozlišené (adresa je redigovaná a PCAP není). WAN nezávislost se netestovala a cloudové vyjednání nové session je stále požadované.

Krátký pokus o ověření fyzické HA HLS cesty 2026-11-07 10:01 CET nedošel do HA: lokální WSLc/WSL runtime vrátil `E_ACCESSDENIED` před spuštěním distribuce a HA port 8123 neposlouchal. Navazující kontrola hlavního HA NUC uspěla read-only přes SSH. Konkrétní RTSP endpoint posledního L2 běhu (`192.168.3.178:53538`) nyní nemá běžící listener; TCP pokus z HA SSH prostředí timeoutoval po 3 s. Registry dostupného HA neobsahují fyzickou C201 a živé stavy přes jeho API bez tokenu vrátily HTTP `401`. Tyto výsledky dál znamenají, že HA HLS není ověřené.

Pozdější chyby DNS (`NativeErrorCode=5` a `nslookup` timeouty) vznikly v omezeném Codex shellu a neprokazovaly selhání hostitelského Windows DNS. Ověření v hostitelském kontextu přes `192.168.3.88` úspěšně přeložilo `google.com`, `account.xiaomi.com` a `de.api.io.mi.com`. Následný přímý RTSP test přes gateway úspěšně obnovil Xiaomi účet, našel jednu C201 v inventáři 4 kamer a vyjednal Xiaomi session. První RTSP `DESCRIBE` přišel před připravením zdroje a skončil `503 Service Unavailable` (0 snímků, kód `-1482175992`, přibližně 0,06 s). Po zaznamenání `session_open`, `first_media` a `rtsp_description ready` FFmpeg s `-an` a výstupem do `null` dekódoval 5 snímků za 3,82 s, návratový kód 0. Protokolové logy z téhož běhu ukazují CS2 `lan_search` 5 ms, `punch` 4 ms (jeden dodatečný typ `0x41`) a úspěšně zvolený `cs2+tcp`; následovalo médium HEVC. To ukazuje na příliš časný první RTSP požadavek; DNS nebylo překážkou. Kamera mluví s gateway přes CS2/MISS, RTSP je výstup gateway. Proces byl zastaven, token odstraněn a síťová pravidla zůstala beze změny. Podrobnosti jsou v `ZAZNAM_VYSLEDKU_C201.md` částech H–N.

Přímé RTSP přes gateway dříve dekódovalo pět snímků. Před navazujícím během anonymní HTTPS požadavek na `account.xiaomi.com` timeoutoval po 8 s, zatímco `de.api.io.mi.com` odpovědělo HTTP 401 za 133 ms (HTTPS dosažitelné, bez autentizace očekávané odmítnutí). Třetí headless běh poté úspěšně obnovil účet, načetl právě jednu fyzickou C201 ze živého inventáře (profilový fallback nebyl potřeba), vyjednal CS2 a otevřel `cs2+tcp`; RTSP cesta se připravila. FFmpeg dekódoval jeden snímek před PTZ (160×90 šedotón, rc 0, 3,10 s) a jeden po jediném samostavicím kroku doleva (rc 0, 1,33 s). Porovnání našlo velkou pixelovou změnu, ale globální posun pevných prvků **nepotvrdilo**: nejlepší posun byl na hranici hledání (+12, −6) a zlepšení shody jen 10,6 % (prahová hodnota 15 %). Pohyb proto zůstává neověřený. Obraz/zvuk se neuložil ani nezobrazil; síťová pravidla a konfigurace se nezměnily. Sanitizovaný přímý výstup je v `.test-env/evidence/c201-motion-headless-attempts-20261107.log`. HLS vydané Home Assistantem zůstává odlišná a neověřená cesta kvůli WSL `E_ACCESSDENIED` a chybějící potvrzené C201 entitě v dostupném HA. WAN blokaci neprovádět.

Následný souvislý RTSP pokus v přibližně 20:20–20:21 obnovil účet, ale timeoutoval při živém inventáři; pomocí ověřené lokální C201 identity pokračoval k žádosti o čerstvé CS2 klíče, která na `de.api.io.mi.com` rovněž timeoutovala. Relace se neotevřela, souvislé FFmpeg dekódování ani robustní párování pevných bodů nezačalo a PTZ příkaz se neposlal. Pro tento běh jsou počty snímků 0. Záznam je v části T výsledků a v sanitizovaném logu.

Poslední omezený headless pokus po krátké prodlevě (20:29 CET) skončil dříve: samotná Xiaomi account restore fáze timeoutovala na `account.xiaomi.com`. Dekódování 640×360, předběžné párování, CS2/RTSP, počty před/po i PTZ jsou pro tento běh 0. Další opakování se neprovedlo. Přímý výstup je v sekci U výsledků.

Následný desetisnímkový hostitelský pokus s ověřenou binárkou SHA-256 `9F1F3D10510EB7D5F981DE4DFFF512CF7D5A7E4685C73BF2B654094BBCD48615` skončil při account restore: sanitizovaný gateway log hlásí `reason=http_request`. Inventář ani RTSP/FFmpeg se nedostaly na řadu, počet snímků i PTZ příkazů je 0. Kódový audit potvrzuje, že levý krok mapuje na `0x112` / `{"operation":1}` na kanálu 0, ale `Step` nečeká na `0x113`; přijaté reply lze číst přes `Session.Replies()`. Fyzická kontrola Xiaomi Home z fáze 0 zatím není potvrzena, proto se zápisový PTZ krok neposílal. Záznam je v části V výsledků; detailní navazující plán je `C201_PTZ_navazujici_plan.md`.

Offline fáze 2 je připravená: `Session.DiagnosticMotorStep(direction)` hlásí odděleně výsledek zápisu a dekódovanou `0x113` nebo `no_reply` po 2 s a emituje pouze povolená sanitizovaná pole. Cílené offline testy prošly s falešným transportem a bez sítě/kamery. Žádný živý PTZ krok se neodeslal. Důkaz je v části W výsledků a `.test-env/evidence/c201-ptz-offline-verify-20261107.log`.

Navazující offline kontrola opravila hraniční případ: `0x113` s časem přijetí za 2 s se nyní označí jako `no_reply` i při pozdním probuzení sondy. `Provider.Step()` i headless `ptz-once.go` používají sanitizovanou diagnostiku na stejné video session. Cílený test falešného transportu, simulované směrování headless helperu i compile-only provideru prošly; živý příkaz se neposlal. Podrobnosti jsou v části X výsledků.

## 1. Sběr výchozích údajů (před všemi testy)

Z Xiaomi Home / štítku / DHCP tabulky zapište:
- Prodejní označení: Xiaomi Smart Camera C201; MIoT model: `xiaomi.camera.c201` (ověřit, jak jej vidí účet).
- Firmware včetně úplného čísla, stav aktualizace (neaktualizovat během diagnostiky), výrobní HW revizi, pokud je dostupná.
- Region Xiaomi Home (např. Evropa), verzi Xiaomi Home a OS telefonu.
- IP, masku, gateway a MAC kamery; IP, masku a připojení PC; SSID/AP kamery.
- Které funkce skutečně fungují v Xiaomi Home: live obraz, otočení v obou osách, mikrofon, zvuk do kamery, noční režim, SD playback.
- Zda jde o stejné L2, jsou-li přítomny mesh/extender, guest síť, client isolation, VPN, VLAN a pravidla blokující přímou komunikaci.

**Ověření:** otevřete a zavřete živý obraz v Xiaomi Home, jednou proveďte malý PTZ pohyb a vraťte kameru. Mi Home pak úplně zavřete (nejen minimalizovat). Pokud nefunguje ani Xiaomi Home, nepokračujte v ladění alternativního klienta.

**Interpretace:** problém již v oficiální aplikaci = napájení / Wi-Fi / účet / region / firmware. Fungující Xiaomi Home = kontrolní funkční reference, nikoliv důkaz, že externí klient bude podporován.

## 2. LAN a základní porty

Použijte PowerShell skript `C201_Network_Probe.ps1` z balíčku:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\C201_Network_Probe.ps1 -CameraIP 192.168.X.Y
```

Alternativní ruční příkazy:

```powershell
ipconfig /all
ping 192.168.X.Y
arp -a
Test-NetConnection 192.168.X.Y -Port 554
Test-NetConnection 192.168.X.Y -Port 80
```

Je-li nainstalovaný Nmap, volitelně:

```powershell
nmap -sT -Pn -sV -p 80,443,554,8554,8080,8000,54321 192.168.X.Y
```

**Interpretace:** ping bez odpovědi není důkaz, že kamera není dostupná (ICMP může být filtrované). `closed` na TCP 554 neznamená, že kamera neumí lokální video: může používat proprietární UDP/TCP transport iniciovaný klientem. UDP 32108 je známé z vyhledávání CS2 u jiných Xiaomi kamer; výsledek obecného port scanneru není vhodný důkaz o jeho podpoře. Uvádějte výsledek přesně, ne jako „kamera RTSP nepodporuje“.

Pokud síť nesedí: přesuňte PC do stejné podsítě/AP, ověřte Windows Defender Firewall pro aplikaci XiaomiViewer (soukromé sítě), izolaci Wi-Fi klientů a firewall mezi VLAN. Potom opakujte stejný test.

## 3. Nejdůležitější diagnostika: Xiaomi Camera Viewer

Zdroj: https://github.com/lubos-hnanicek/xiaomi-camera-viewer
Průvodce: https://github.com/lubos-hnanicek/xiaomi-camera-viewer/blob/main/docs/camera-diagnostics.md
Releases: https://github.com/lubos-hnanicek/xiaomi-camera-viewer/releases

1. Stáhněte aktuální Windows x64 balíček z oficiálních Releases, rozbalte lokálně; použijte `XiaomiViewer.exe`. Nepoužívejte neoficiální náhodné binárky.
2. Přihlaste stejný Xiaomi účet a **stejný region** jako Xiaomi Home. Dokončete 2FA/CAPTCHA podle potřeby.
3. Ověřte, zda se C201 zobrazuje v `Cameras -> Refresh from account`. C201 nemusí být podporovaná pro přímé přehrávání; pro diagnostiku to nevadí.
4. Ukončete živý obraz v Xiaomi Home, případně SD playback a nahrávání ve Vieweru; pak v `Help -> Camera diagnostics` vyberte C201 a `Collect diagnostics` (základní varianta bez volitelných sond).
5. Výstup naleznete pod `%APPDATA%\XiaomiViewer\Diagnostics`. Vyplňte `owner-report.txt` a uložte kopii ZIP.
6. Neodesílejte balíček na veřejné fórum bez kontroly osobních údajů.

Jestliže se GUI sonda nespustí, lze po přihlášení a zavření Vieweru použít v rozbaleném adresáři:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\collect-camera-diagnostics.ps1
```

Nástroj by měl poskytnout:
- `summary.json`: model, region, průběh, selhání, omezení měření.
- `device.json`: záznam vybraného zařízení po redakci identifikátorů.
- `miot-spec.json`: modelově správná specifikace (pokud se podaří).
- `communication.json`: transportní vyjednávání, MIoT read-only odpovědi, MISS/RDT reakce, hlavičky a statistiky médií.
- `owner-report.txt`: ručně doplněné skutečnosti.

**Volitelné parametry až po prvním vyhodnocení** (netestovat všechny kombinace naráz): `-Quality 1` nebo `-Quality 3`, `-Transport tcp`, případně `-IncludeFirmwareInfo`. Záznam SD katalogu (`-IncludeStorage`) zpravidla není pro první live video potřebný a může obsahovat časy nahrávek.

### Vyhodnocení souborů

| Hledat | Výsledek | Co znamená | Další krok |
|---|---|---|---|
| `summary.json` | Kamera v seznamu chybí | Chybný region, účet, relace či seznam zařízení | ověřit přihlášení/region, ne protokol |
| `communication.json` | CS2 (UDP/TCP) + rozpoznaný MISS | známe slibnou kombinaci transportu a formátu | test go2rtc, potom profily |
| `communication.json` | TUTK / Agora / MTP / neznámý vendor | není podklad pro slepé CS2 pokusy | uložit vendor výsledek; cílený capture oficiálního klienta |
| Handshake | timeout před odpovědí | může být firewall, izolace, discovery nebo nepodpora | ověřit L2, UDP a zachytit paketovou stopu |
| Handshake | autentizace/klíče odmítnuty | klient dojde dál, ale nedohodne bezpečnou relaci | zkontrolovat token/region, neprovádět brute-force |
| Relace | otevřená, media headers a počty rámců rostou | stream pravděpodobně teče | vyzkoušet dekódování a profil |
| Relace | otevřená, bez rámců | profil/kanál, START příkaz, firmware, souběžné spojení | výběrové kvalitativní sondy |
| Media frames > 0, obraz černý | dekódování/codec, vadné parametry nebo chybějící keyframe | vyčíst kodek, ffprobe, zkusit jiný profil |
| MIoT vlastnosti se čtou | model má dostupnou cloudovou/MIoT control-plane cestu | nevypovídá automaticky o plně lokálním přístupu | později ověřit změnu bez WAN |

Poznámka: `dropped frames` v diagnostickém nástroji může být pouze přeplněná interní fronta, ne ztráty paketů ve Wi-Fi. Nulový počet odpovědí sám o sobě neurčuje příčinu. Důležitá je *fáze*, v níž spojení selhalo.

## 4. Druhý klient: go2rtc

Zdroj: https://github.com/AlexxIT/go2rtc
Xiaomi dokumentace: https://github.com/AlexxIT/go2rtc/blob/master/internal/xiaomi/README.md
Known C201 issue: https://github.com/AlexxIT/go2rtc/issues/2290

Pro první diagnostiku doporučuji Windows portable místo Dockeru: minimalizujeme rozdíly mezi síťovými režimy. Stáhněte `go2rtc_win64.zip` (aktuálně ověřena řada `v1.9.15` ke dni 2026-11-07) a rozbalte do samostatné složky. Spusťte `go2rtc.exe` a otevřete `http://127.0.0.1:1984/`.

1. `Add -> Xiaomi`, přihlásit Xiaomi účet, případně 2FA.
2. Vybrat skutečnou C201 a vytvořit stream přes UI, nezadávat vymyšlené RTSP adresy; **generované Xiaomi URL obsahuje přihlašovací údaje/session údaje**, nesdílet.
3. Pokud vznikne např. stream `c201`, pokus o náhled v go2rtc WebUI. Zaznamenat přesné chyby z konzole/Log, ale redigovat tokeny.
4. Pokud se naváže spojení, zkusit kvality `subtype=1`, `2`, `3` (potom případně `0`, `4`, `5`) **po jedné**. Sledovat rozlišení, snímky za sekundu, audio a stabilitu. Typy v Xiaomi nejsou jednotné napříč modely.
5. Je-li stream úspěšně zveřejněn pod `c201`, přes ffprobe/ffplay lokálně:

```powershell
ffprobe -v error -show_entries stream=index,codec_type,codec_name,width,height,r_frame_rate -of json rtsp://127.0.0.1:8554/c201
ffplay -rtsp_transport tcp rtsp://127.0.0.1:8554/c201
```

Pokud ffprobe ještě není nainstalované, stačí WebUI. RTSP výstup je **z go2rtc**, nikoli důkaz nativního RTSP serveru v C201.

**Výklad chyb:**
- `unsupported model/vendor/protocol`: především problém podpory C201 v klientovi, ne nezbytně v LAN.
- `timeout/no answer`: L2 discovery, UDP firewall, souběžné spojení, AP/client isolation nebo handshake; podívat se do diagnostiky Vieweru.
- `authentication/verification failed`: účet/region/token/nové přihlášení; nemažte náhodně celé zařízení.
- `connected` bez médií: test kvality / transportu / kanálu, ne přepis autentizace.
- Frames a zvuk, ale bez obrazu: nejdřív ověřit codec H.264/H.265 a dekodér/ffprobe.

Na go2rtc **neumisťujte přihlašovací údaje do veřejně dostupného UI**. Zatím porty nevystavujte do internetu.

## 5. Test PTZ a zvuku — až po fungujícím obrazu

Vyzkoušejte PTZ nejprve v Xiaomi Home jako referenci, potom v externím klientovi *jen pokud nabízí explicitní ovládání dané kamery*. Proveďte malé bezpečné pohyby doleva/doprava a nahoru/dolů, zapište odezvu v milisekundách, směr, možnost STOP a stabilitu streamu. Zvlášť ověřte odposlech mikrofonu a případný talkback (není to totéž).

Pozor: MIoT `ptz-calibrate` je kalibrace, nikoli běžný příkaz k pohybu. Na nepodporované C201 neposílejte neznámé hodnoty akčních příkazů. Pokud je video hotové, ale PTZ není dostupné, označte výsledek **L2 + PTZ pending** a nezastavujte práce na obrazu.

## 6. Čtyři offline testy — měřit novou relaci

**Předpoklad:** kamera a externí klient již prokazatelně fungují s WAN. V routeru blokovat pouze **internet (WAN)** a ponechat lokální LAN, DHCP a potřebné lokální služby. Změny jsou vratné. Každé blokování provádějte na zadaném zařízení, nikoli globálně na domácí síti.

| Pokus | WAN pro kameru | WAN pro PC | Akce | Interpretace |
|---|---|---|---|---|
| A | povolen | povolen | nově spustit stream | referenční funkční relace |
| B | zakázán | povolen | ukončit původní relaci; otevřít novou | ověření, zda kamera potřebuje WAN při relaci |
| C | povolen | zakázán | ukončit původní relaci; otevřít novou | ověření, zda klient vyžaduje cloudové klíče |
| D | zakázán | zakázán | ukončit, spustit klienta a kameru znovu; otevřít novou | skutečný offline důkaz, pokud funguje |

Opakujte D po **restartu PC klienta i kamery**, aby nezůstala platná stará relace / klíče. Pouze pokračování dříve otevřeného streamu není offline autentizace. Pokud C neuspěje a B ano, pravděpodobně klient potřebuje cloudové klíče. Pokud selže jen B, kamera může vyžadovat cloudové služby nebo blokovací pravidlo odpojilo víc než WAN. Vždy ověřte, že LAN provoz nebyl zablokován omylem.

## 7. Pokročilá diagnostika pouze při neúspěchu

### Pokud selže discovery/handshake

1. Zapnout Wireshark na rozhraní **toho PC, které skutečně spouští XiaomiViewer/go2rtc**.
2. Display filter např. `ip.addr == 192.168.X.Y` (kamera) nebo `udp.port == 32108` při hledání discovery.
3. Znovu spustit *jeden* pokus o připojení, zaznamenat čas včetně sekund.
4. Zkontrolovat: odchází UDP discovery? Na unicast/broadcast? Odpovídá kamera? Na jakém zdrojovém portu? Následuje TCP/UDP komunikace? Kde provoz utichá?
5. Pokud kamera odpovídá pouze na directed broadcast, má smysl upravit discovery klienta; pokud odpověď není, testovat samotnou síť a jiný přístupový bod.

Důležité: Wireshark na PC **běžně neuvidí cizí unicast komunikaci telefonu s kamerou** na přepínané síti. Pro její zkoumání je potřeba mirror/SPAN na switchi, monitorovací AP/router nebo vhodný Android capture. Při zapnutém VPN capture se může změnit samotné chování LAN discovery.

### Pokud je transport jiný než známé CS2/MISS

Pokud Wireshark/verifikovaná diagnostika ukáže Agora/MTP/jiný vendor, už neeskalujte náhodnými kvalitami. Sbírejte raději *cílené* stopy Xiaomi Home: 1. spuštění videa, 2. jeden pohyb PTZ, 3. změna kvality a 4. ukončení streamu. Každou operaci pořizovat samostatně, s časovou značkou. Neobcházet TLS a neodesílat účetní klíče. Lze prověřit pouze veřejnou/originální oficiální firmware URL a dostupný plugin Xiaomi Home v souladu s licencí. Žádný flash ani pokusy o exploit v první fázi.

## 8. Jednoznačný rozhodovací strom

```text
Xiaomi Home obraz/PTZ funguje?
  NE -> vyřešit napájení, Wi-Fi, účet, region; STOP
  ANO -> stejná L2 pro PC a kameru; Viewer diagnostics
      Kamera není v device list? -> účet/region/synchronizace; zopakovat
      Kamera je v device list:
        Transport CS2 + MISS (či rozpoznaný podporovaný)?
          ANO -> go2rtc stream -> media headers?
             NE -> síť/handshake/auth, one-variable test, packet capture
             ANO -> video dekóduje?
                NE -> profil/codec/keyframe, ffprobe
                ANO -> L2 splněno -> audio/PTZ -> L3 -> offline matice -> L4
          NE, Agora/MTP/TUTK/unknown -> zdokumentovat vendor+fázi selhání,
               nepokračovat ve slepém ladění portů, cílený capture Xiaomi Home
```

## 9. Co v této fázi nepovažovat za důkaz

- Otevřený TCP port 554 sám o sobě **nedokazuje** funkční RTSP.
- Zavřený TCP port 554 **nevylučuje** přenos videa v LAN jiným protokolem.
- Dostupná MIoT specifikace **nedokazuje**, že její vlastnosti lze číst a měnit bez cloudu.
- Fungující video při WAN blokaci **nedokazuje** offline autentizaci, pokud relace vznikla před blokací.
- Úspěch konkrétního modelu C200/CW300/CW400 **nedokazuje** podporu C201.
- Hodnota `dropped frames` diagnostického collectoru **není sama o sobě ztráta Wi-Fi paketů**.

## 10. Jaké výsledky připravit pro další rozhodnutí

Doporučené soubory (redigované): `01_baseline.md`, výstup `C201_Network_Probe.ps1`, `summary.json`, `device.json`, `miot-spec.json`, `communication.json`, `owner-report.txt`, log go2rtc (bez secretů), tabulka pokusů o kvalitu a případně malý účelový PCAP. Vyplňte `ZAZNAM_VYSLEDKU_C201.md`.

Jakmile máme **model+firmware, způsob transportu, handshake stage, typ chyby a počty přijatých médií**, umíme rozhodnout, zda jde o *pouhé doplnění C201 do existujícího klienta*, *nový variant příkazu/start-streamu*, *problém s LAN discovery*, nebo *jiný proprietární transport vyžadující výzkum*. Teprve poté má smysl zadat implementaci HA integrace.

## Ověřené zdroje (2026-11-07)

- Oficiální parametry C201: https://www.mi.com/cz/product/xiaomi-smart-camera-c201/specs/
- MIoT specifikace: https://home.miot-spec.com/spec/xiaomi.camera.c201
- Xiaomi Camera Viewer README: https://github.com/lubos-hnanicek/xiaomi-camera-viewer
- Xiaomi Camera Viewer Diagnostics: https://github.com/lubos-hnanicek/xiaomi-camera-viewer/blob/main/docs/camera-diagnostics.md
- go2rtc Xiaomi: https://github.com/AlexxIT/go2rtc/blob/master/internal/xiaomi/README.md
- go2rtc C201 issue: https://github.com/AlexxIT/go2rtc/issues/2290
- go2rtc releases: https://github.com/AlexxIT/go2rtc/releases
