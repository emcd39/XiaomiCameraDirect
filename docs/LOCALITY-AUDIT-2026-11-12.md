# Ověření lokálnosti komunikace — 12. 11. 2026

Výchozí commit: `3cb89e8` (`Add read-only C201 settings diagnostics`). Kontrola pouze čtením kódu, existujících výsledků a stavu procesů. Zdrojový kód ani nastavení kamery se v tomto zadání nemění. Navazující práce: [podrobný plán](LOCAL-CAMERA-HANDOFF.md).

## Závěr

**Současné řešení není plně lokální a jeho nezávislost na internetu není prokázaná.** Šest nových stavů C201 se čte přes Xiaomi cloud. Přihlášení, inventář a získání autorizačních údajů pro novou mediální relaci rovněž používají cloud. Vlastní implementace CS2 míří na adresu kamery z inventáře; video a přímé PTZ sdílejí toto spojení. V prohlédnuté CS2 cestě není implementován fallback na cloudový mediální relay. To je zjištění o kódu, nikoli aktuální síťové měření ani zákaz všech jiných síťových cest.

Historické záznamy dokládají dekódované C201 video a čtyři směry PTZ. Samotné `cs2+tcp`, úspěšný PTZ, soukromá IP ani lokální RTSP URL neprokazují provoz bez cloudu. Pro úplný důkaz chybí korelace socketů/paketů a řízený WAN test kamery i gateway.

## Doložené cesty v aktuálním zdroji

| Operace | Cesta | Co je ověřeno | Co závěr neznamená |
| --- | --- | --- | --- |
| Konzole / HA → gateway | Lokální HTTP API v testované instalaci | Launcher připravuje loopback; historie uvádí `api_exposed=false` | Loopback první části řetězce neříká nic o gateway → Xiaomi |
| Obnovení Xiaomi účtu | Cloud | `Provider.New` volá `Registry.Restore`; Xiaomi cloud klient obnovuje relaci | Uložený token neznamená start bez internetu |
| Inventář účtu | Cloud při načtení | `cameraDevices` využívá účet a seznam zařízení | Lokální cache není aktuální potvrzení dosažitelnosti kamery |
| Příprava nové mediální relace | Cloud + kamera | `cameraSource.start` volá `MissVendor` → `/v2/device/miss_get_vendor`, pak `stream.Open` | Běžící stream může přežít WAN výpadek, nový stream přesto nemusí nastartovat |
| Video kamera → gateway | Přímý CS2 na `device.IP` podle kódu | `miss.Dial` → `cs2.Dial`; UDP discovery/punch, případně TCP na dohodnutý port stejné IP | Dosavadní logy nedokládají kompletní síťovou trasu ani chování kamery vůči jejímu vlastnímu cloudu |
| PTZ krok | Command channel existující CS2 session | `Provider.Step` → `DiagnosticMotorStepObserved` → MISS `0x112`; odpověď `0x113` | Příprava chybějící relace může nejdříve potřebovat cloud; PTZ není automaticky funkční při studeném offline startu |
| Night Shot + pět přepínačů | **Cloud** | `CameraSettings` → `MiotGetContext` → `/miotspec/prop/get` → regionální HTTPS server | Lokální GET `/v2/cameras/{id}/settings` tuto cestu nepřevádí na LAN |
| Snímek / lokální soubor | Z lokálního restreamu | Gateway/konzole dekóduje či ukládá již přijatá média | Obnovování zdrojové relace může mít cloudovou závislost |
| SD index / přehrávání | Nezapojeno do produktu | Obecná RDT/Playback implementace existuje | C201 kanál, přijetí requestu a účinná funkce nejsou ověřené |
| Motion/person/pet události | Nezapojeno | Existují deklarace MIoT vlastností/událostí | Čtení `enabled=true` není doručení události |

### Přesná místa pro opakování kontroly

- [provider.go](../internal/xiaomi/provider.go): `New`, `cameraDevices`, `CameraSettings`, `cameraSource.start`, `Step`, `readFrames`, `mediaTransportPreference`.
- [cloud/api.go](../internal/xiaomi/protocol/cloud/api.go): `GetBaseURL`, `requestContext`, `MissVendor`, `MiotGetContext`. Pro region `de` je základ `https://de.api.io.mi.com/app`; ostatní podporované regiony mají vlastní shard.
- [miss/client.go](../internal/xiaomi/protocol/miss/client.go): `Dial`, `login`, `MotorStep`. Aktuální klient obsahuje CS2, nikoli implementaci TUTK.
- [cs2/conn.go](../internal/xiaomi/protocol/miss/cs2/conn.go): `handshakeOnPort`, `WriteUntil`, `newTCPConn`, `udpConn.Read`.
- [stream/stream.go](../internal/xiaomi/protocol/stream/stream.go): `Open` již má `Protocol` a `RemoteAddr`; aktuální provider zpřístupňuje protokol v logu, ne kompletní audit trasy.
- [Prepare-Test-Camera.ps1](../xiaomi-diagnostic-console/scripts/Prepare-Test-Camera.ps1): připnutý historický gateway EXE hash. Není důkazem, že launcher spouští zdroj z commitu `3cb89e8`.

### Meze síťové ochrany, které další práce musí řešit

1. `net.ParseIP(host)` v provideru ověřuje syntaxi, ne příslušnost k povolené síti ani identitu kamery. Přímý socket na IP proto není sám o sobě vynucený režim „jen LAN“.
2. Handshake `WriteUntil` odmítá odpovědi z jiné IP. V běžném `udpConn.Read` je ale podmínka `addr.IP.Equal(c.addr.IP) || n >= 8`; cizí datagram dostatečné délky touto podmínkou projde. V plánu je cílená negativní regrese a oprava po ověření požadované sémantiky portů. Tato kontrola neprováděla exploit ani síťovou sondu.
3. HA, gateway, kamera a případná proxy mohou používat odlišné síťové namespace/rozhraní. Privátní rozsah IP ani jméno transportu neodhalí VPN, tunel nebo nepřímý přenos.
4. Nepřítomnost relay implementace v této vlastní CS2 cestě se nesmí přenášet na jiné SDK/adaptéry, které budou přidány později.

## Aktuální read-only kontrola prostředí

Kontrola proběhla 12. 11. 2026 přibližně od 16:04 CET (`+01:00`).

- `git status --short`: sledované zdroje čisté na `3cb89e8`; již před zadáním existovaly neversionované `.codex/` a `xiaomi-diagnostic-console/bin/`.
- `Get-Process` s filtry `xlg`, `diagconsole`, `gateway`, `xiaomi`, `ffmpeg`, `homeassistant` nevrátil odpovídající proces.
- `netstat -ano` neukázal listener na dříve používaných portech `63983`, `8099`, `8123`, `8554`. Tyto porty samy nevylučují jinou instalaci na dynamickém portu.
- Z existujícího `config.live.json` byla v paměti přečtena pouze URL gateway, ověřeno `Uri.IsLoopback` a proveden jeden GET `/healthz` s timeoutem 3 s. Výsledek: `SocketException`. Token nebyl čten ani odesílán. Tato chyba sama nerozliší neběžící službu od omezení shellu.
- Nebyla obnovena cloudová relace, otevřen nový stream, spuštěn motor, zaznamenán obsah kamery ani změněn firewall. Získání aktuálního provozního důkazu zůstává otevřené. Absence procesu neprokazuje absenci uložených přihlašovacích údajů.

## Historická evidence a její hranice

- [TEST_REPORT.md](../xiaomi-diagnostic-console/docs/TEST_REPORT.md), oddíly „Přímé PTZ ovládání…“, „Vazba PTZ kliknutí…“ a „Průzkum dalších funkcí…“: čtyři fyzické směry potvrzené jednotlivými zápisy/odpověďmi a obrazem; později i krátký dekódovaný náhled. Neobsahují průkazný WAN-offline test.
- [IMPLEMENTATION-STATUS.md](IMPLEMENTATION-STATUS.md), poslední oddíly: šest nastavení implementováno; cílený Go route test prošel; HA 2026.10.4 E2E dosud nespustil testy kvůli `E_ACCESSDENIED` ve WSLc.
- [Návrh SD](../xiaomi-diagnostic-console/docs/C201_SD_READONLY_DESIGN.md): staré MIoT výsledky `status=1`, kapacity `0,0,0` neobsahují jednotky ani potvrzený význam stavu.
- Starší úvodní tabulky v `COMPATIBILITY.md` a `DELIVERY-PLAN.md` předcházejí živému PTZ ověření. Nepoužívat je k tvrzení, že přímé PTZ dosud nikdy nefungovalo. Současně nepřenášet úspěch Windows konzole na HA/Linux.

## Jak lokálnost skutečně změřit při implementaci

Tyto kroky jsou návrh, v tomto zadání nebyly spuštěny. Přesný pracovní postup a záznamy stanovuje úkol A03 v předávacím plánu.

1. Ověřit identitu procesu, build hash, model/firmware, aktuální adresu kamery a síťovou trasu. Nepřebírat staré PID/IP/porty. Použít autorizovanou testovací relaci.
2. Získat lokálně metadata spojení procesu i provozu testovací kamery na routeru/zrcadleném portu. Pro TCP využít tabulku spojení; pro UDP jsou nutné metadata datagramů, samotný `netstat` protistranu nedokazuje. Veřejný report obsahuje třídu cíle a korelaci, soukromý report skutečné adresy.
3. Korelovat `operation_id`, čas, lifecycle session, peer, počty bajtů, čerstvé dekódované snímky. Cloudové řízení vykázat odděleně od datového toku. Nepovažovat všechno TCP/443 za video ani všechno UDP za LAN.
4. V izolované testovací síti zaznamenat baseline, omezit WAN kamerě a gateway, ověřit účinek pravidel i zachovanou vzájemnou LAN. Zahrnout IPv4 i IPv6 a možné tunely. Testovat nejdřív běžící relaci, potom novou relaci ve stejném procesu, poté restart testovací gateway s uloženým účtem; restart kamery je samostatný řízený test.
5. Pro každou fázi uvést `pass`, `fail` či `blocked`, délku měření, dostupné cloudové závislosti a stáří snímků. Neúspěch kvůli cloudovému podpisu nevyhodnotit jako nefunkční lokální datový tok. Po každém pokusu automaticky obnovit pouze pravidla zavedená testem a ověřit obnovu služby.
6. Cílové tvrzení uvádět odděleně: `media_direct_observed`, `control_direct_observed`, `warm_session_without_wan`, `new_session_without_wan`, `gateway_restart_without_wan`, `camera_restart_without_wan`, `settings_read_without_wan`. Neexistuje jedno univerzální „local=true“ pro celý produkt.

## Externí podklady

Vlastní README [Xiaomi Camera Viewer](https://github.com/lubos-hnanicek/xiaomi-camera-viewer) rozlišuje přímý video přenos a cloud pro účet, inventář, klíče a nastavení. Je to podpůrný popis upstreamu; výše uvedené závěry o našem commitu vycházejí z místního kódu. Webový obsah byl konzultován 12. 11. 2026 a není důkazem živé C201 relace.
