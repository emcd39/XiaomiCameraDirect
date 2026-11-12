# Lokální funkce a rozšíření podpory kamer — předání pro levný model

Datum: 12. 11. 2026. Výchozí zdroj: commit `3cb89e8` v `C:\DEV\xiaomi-local-gateway`.

**Toto je plán budoucí implementace. Aktuální zadání dovoluje průzkum a dokumentaci, nikoli změny kódu.** Nové názvy rozhraní, příkazy diagnostiky a cesty označené jako návrh zatím neexistují. Nespouštěj je jako hotové nástroje. Samotný tento dokument neschvaluje nové zásahy do kamer, routeru ani produkčního HA.

## 1. Cíl, priority a pravidla práce

Cíl: dokončit C201, měřit skutečnou lokálnost každé funkce a vytvořit opakovatelný postup, jak přidat další konkrétní kameru bez rozbití těch funkčních. Výsledkem nemá být obecný ovladač, který naslepo posílá všechny známé příkazy na libovolné zařízení.

Priorita: **reprodukovatelné testy → diagnostika a důkaz trasy → stabilní obraz/HA → další funkce C201 → první další model stejného transportu → starší transporty → složitější zařízení → vydání.** Externí blokátor jedné větve nebrání práci na nezávislé offline úloze. Nezačínej ale implementaci závislou na neověřeném protokolu odhadem.

### Pracovní smlouva pro implementátora

1. Vyber právě jednu kartu úkolu níže. Nejdřív přečti uvedené zdroje a poslední záznam úkolu. Napiš konkrétní očekávaný výsledek a soubory, do kterých sáhneš.
2. Jedna změna řeší jeden pozorovatelný výsledek. První část je protokol/provider a test, další část API/HA, poslední část živé ověření. Nemíchej do ní nový frontend, změnu loginu, reorganizaci repozitáře nebo aktualizaci závislostí.
3. Začínej existujícím kódem. Nevytvářej druhého vlastníka kamerové relace, paralelní reconnect smyčku ani další gateway jen proto, že chybí endpoint.
4. Když musíš změnit soubor mimo vyjmenovaný rozsah, zapiš důvod a vytvoř malý předpoklad daného úkolu. Nepřepisuj tím celý plán. U běžného lokálního refaktoru v autorizovaném rozsahu se zbytečně nevracej pro souhlas.
5. Dva stejné neúspěchy bez nové informace ukončují daný experiment. Další pokus musí změnit jednu pojmenovanou proměnnou a mít novou hypotézu. `E_ACCESSDENIED`, chybějící nástroj a neexistující hardware jsou blokátory prostředí, nikoli oprávnění měnit očekávání testu.
6. Testuj změněné chování, chyby a souběh. Po průchodu relevantních testů je neopakuj bez nové změny nebo nejasnosti. Syntaktická kontrola, mock, odpověď HTTP a fyzický účinek jsou odlišné důkazy.
7. Každá živá operace používá konkrétní model/firmware a aktuálně potvrzenou identitu kamery. Staré PID, IP, token file a build hash nejsou výběr cíle. Nepřepisuj stávající API v1 nebo ID entit kvůli nové funkci.
8. Zápis, pohyb, mikrofon/reproduktor, ukládání snímků, výpadek WAN a nasazení podléhají skutečnému rozsahu uživatelova zadání. Existující souhlas znovu nevyžaduj, pokud pokrývá stejný cíl a operaci. Souhlas s jedním starým audio/PTZ pokusem není trvalý souhlas pro všechny kamery.
9. Neprováděj brute force command IDs, MIoT adres, RDT kanálů, hesel ani profilů. Neprováděj flash, factory reset, kalibraci nebo formátování SD. Neber hlášení „write OK“ jako důkaz funkce.
10. Po splněném úkolu vytvoř malý checkpoint podle aktuálního oprávnění pro Git. Výsledek hlásíš jako `implementováno / offline ověřeno / zařízení ověřeno / HA ověřeno / blokováno`. Nezapisuj jednotné „hotovo“, když chybí požadovaný důkaz.

### Ochrana již funkčního PTZ a konzole

Povinně zachovat: kliknutí nese konkrétní camera ID, zastaralý výběr vrací 409 bez zápisu, náhled a šipky patří stejné kameře, prázdný serverový výběr vypíná šipky, starý polling nesmí vrátit minulý inventář, pomalé obnovy mají nejvýše jeden aktivní a jeden čekající cyklus a dokončená akce nečeká nekonečně na další tiky. PTZ funguje i s vypnutým preview. Jeden klik = nejvýše jeden zápis; nejasný timeout se automaticky neopakuje. Mock nesmí kontaktovat gateway/kameru.

## 2. Ověřený výchozí stav a mapa zdrojů

| Oblast | Stav k výchozímu commitu | Další mezera |
| --- | --- | --- |
| C201 `xiaomi.camera.c201`, FW `5.3.2_0016`, region `de` | Historicky dekódované H.265 a čtyři fyzické směry přes Windows konzoli | Aktuální relace, HA/Linux přejímka, měření síťové trasy |
| Night Shot + pět enable stavů | Autorizovaný endpoint a HA entity v kódu, nový route test prošel | **Čtení jde přes cloud**; skutečný HA 2026.10.4 běh není ověřen |
| Obnova schopností po chybě `/v2/info` | `async_refresh()` a pozdní registrace, E2E ověřuje skutečný `entry_id`, `loaded` a jednotlivé stavy | Spustit tento test v Linuxu; WSLc dříve končí `E_ACCESSDENIED` před testy |
| Vlastní Xiaomi provider | Povoluje C201 a vendor CS2, `Audio=false`, `Quality=auto`, `Channel=0` | Per-model profily; provider odmítá jiné video kodeky než H.265 |
| TUTK a starší formáty | V této převzaté protokolové vrstvě vynechány | Samostatné adaptéry; přidání modelu do seznamu nestačí |
| Záznam souborů v konzoli | Existující lokální mechanismus | Průkazná K3 přejímka reálného klipu; 0 lokálních souborů neříká nic o SD |
| SD index/fetch | Nižší vrstva existuje; generický FileRequest 24 B otestován | C201 kanál, odpověď, jednotky kapacity a provider/API |
| Audio, talkback, události, privacy, presets | Některé konstanty/parsery a historická specifikace | End-to-end funkce nejsou tímto doloženy |

Čti podle úkolu, ne celou historii při každém kroku:

- [Audit lokálnosti](LOCALITY-AUDIT-2026-11-12.md): přesné cesty a aktuální omezení důkazů.
- [IMPLEMENTATION-STATUS.md](IMPLEMENTATION-STATUS.md): poslední oddíly nastavení a testů.
- [TEST_REPORT.md](../xiaomi-diagnostic-console/docs/TEST_REPORT.md): nejnovější výsledky konzole; starší neúspěchy nepřebíjejí novější průkazné výsledky.
- [DELIVERY-PLAN.md](DELIVERY-PLAN.md), [SIMPLE-STABLE-PLAN.md](SIMPLE-STABLE-PLAN.md): cílový produkt a T02–T11. Část popisu výchozího stavu je historická; nevracej implementaci na stav z 6. listopadu.
- [SD návrh](../xiaomi-diagnostic-console/docs/C201_SD_READONLY_DESIGN.md): stále platné požadavky na čas, kanál a rozlišení prázdného indexu.
- `internal/xiaomi/provider.go`, `internal/backend/provider.go`, `internal/model/camera.go`, `internal/httpapi/api.go`: produktová hranice.
- `internal/xiaomi/protocol/{cloud,miss,stream,recordings,lan}`: nejnižší vrstva; existující raw helper není veřejné API.
- `internal/streamrtsp/relay.go`, `custom_components/xiaomi_local_gateway/`, `xiaomi-diagnostic-console/internal/console/`: restream a klienti.
- `scripts/test-local.ps1`, `tests/e2e/test_ha.py`, `tests/test_camera_settings.py`, `.github/workflows/ci.yaml`: ověřovací prostředí. Root CI zatím nezahrnuje celý HA E2E ani všechny testy konzole.

## 3. Co se smí označovat jako lokální a podporované

Každá schopnost musí mít odděleně **proveditelnost**, **důkaz** a **závislosti**. Návrh interního záznamu, nikoli současné API:

```yaml
feature: night_mode_read
implementation: implemented     # absent / research / implemented
validation: offline            # none / offline / device / ha
result: unknown                # pass / fail / blocked / unknown
path: cloud                    # lan / cloud / nvr_local / unknown
requires_cloud: [operation]     # provisioning / new_session / operation; nebo []
offline_verified: []            # warm_session / new_session / gateway_restart / camera_restart
model: xiaomi.camera.c201
firmware: 5.3.2_0016
region: de
evidence_ref: <sanitized-run-id>
```

Tento příklad nezakládá nové ověření: settings jsou implementované přes cloud a HA důkaz chybí. `unsupported` používej pro hranici konkrétní implementace nebo doloženou odpověď; chybějící odpověď je `unknown/timeout`, nikoli důkaz, že hardware funkci neumí. Aktuální nedostupnost se nesmí změnit na `off`, `false`, nulovou kapacitu či vymazání podpory.

V prvním vydání lze pravdivě nabídnout lokální média s cloudem pro přípravu relace. „Plně offline“ je samostatná přejímka. Cloudové nastavení může zůstat volitelnou jasně označenou funkcí, ale nepomáhá splnit požadavek lokálního ovládání. Lokální NVR detekce je jiný zdroj než detekce firmwaru kamery.

## 4. Základní úkoly A00–A04

### A00 — Ověřit přesný checkpoint a zprovoznit testovací prostředí

**Závislost:** žádná. **Rozsah:** test runner, `tests/`, případně CI; při blokaci pouze evidence. Žádný nový kamerový příkaz.

1. Zapsat `git rev-parse HEAD`, čistotu stromu, verze Go/Node a image digest HA 2026.10.4. Porovnat zdrojový commit a skutečně používané EXE; launcher má historicky připnutý starší gateway build.
2. Nejprve jeden preflight WSLc. Pokud je runtime skutečně dostupný, spustit existující `scripts/test-local.ps1`. Při `E_ACCESSDENIED` nedělat pátý stejný pokus. Využít již dostupný Linux s Docker/Podman nebo povoleným WSLc; nepřestavovat kvůli tomu produkt.
3. Pokud je nutný Linux runner, přenést jen orchestrace existující sady do **nového** `scripts/test-local.sh`: stejné image digesty z Dockerfile/PS runneru, fixture, testy, slučitelné logy, izolovaný namespace, dočasné credentials, cleanup pouze vlastních zdrojů. Žádné zjednodušení E2E na pouhý import Pythonu. Předem uvést, že `.sh` zatím neexistuje.
4. Testovat skutečnou HA registraci: první `/v2/info` selže; položka je `loaded`; kamera a PTZ jsou dostupné; `/settings` zatím 0. Poté příznak, Night Shot `Auto`, pět stavů konkrétní položky `on/on/off/on/off`; po chybovém MIoT kódu `unavailable`, bez reloadu. Gateway bez příznaku zachová staré funkce a nečte settings.
5. Spustit zvlášť testy nested Go modulu konzole. Root `go test ./...` jej nezahrnuje.

**Přejímka:** skutečné výsledky Go + HA včetně `late-settings`, hashe zdrojů uvnitř runtime, žádný kontakt na fyzické kamery. Pokud runtime není dostupný, `blocked_environment` s konkrétní chybou; lze připravovat A01/A02 offline, ale bez tvrzení o funkčním HA. **Checkpoint:** opravovat pouze chyby způsobené touto funkcí; obecné přepisování platformy patří jinam.

### A01 — Zavést popis schopností po jednotlivých funkcích

**Závislost:** přečtená baseline; offline práce může běžet při blokovaném A00. **Rozsah:** model/backend/provider/API a fixtures, potom HA.

- Zachovat dosavadní boolean capabilities pro kompatibilitu, přidat verzované volitelné podrobnosti: cesta, požadavky na cloud, firmware/profil a dostupnost konkrétní funkce. Neměnit význam existujícího `/v2/info` feature flagu: říká schopnost gateway, ne všech připojených zařízení.
- Přidat strojově použitelný důvod `unverified_firmware`, `unsupported_transport`, `channel_unverified`, `auth_required`, `timeout`, `stale`; chyby dostanou UI text. Pojmenování sladit s existujícími ErrorCode, nevytvářet duplicitní soustavy.
- HA dostane entity jen pro schopnosti dané kamery. Enable senzory zůstanou diagnostické a oddělené od budoucích motion událostí. U cloudové cesty uvést závislost v diagnostice.
- Test: starý v1 klient, neznámá volitelná pole, dvě různé kamery, chybějící schopnost, ztráta spojení bez záměny za vypnuté nastavení. Aktuální příznaky se nevymýšlejí ze jména modelu.

**Přejímka:** jedna fixture popíše C201/cloud settings a lokální video se závislostí na cloudovém setupu; mock či neznámý model nemají falešné `device_verified`. **Checkpoint:** kontrakt a jeho kompatibilní použití.

### A02 — Připravit omezený diagnostický runner

**Závislost:** A01. **Rozsah:** navržené `internal/diagnostics/`, `cmd/camera-probe/`, malé instrumentační body existujícího provideru, fixtures. Specifikace v oddíle 7.

- Využít stávající session manager. Pokud relaci vlastní běžící gateway, probe jí zadává předdefinovanou operaci; neotevírá druhou relaci. Standalone režim dovoluje jen výslovně určené vlastnictví testovací relace.
- Začít read-only preflightem, stavem session a metadaty. Zakázat implicitní zahájení obrazu/audio jen kvůli zobrazení seznamu diagnostických voleb.
- Nezavést veřejné libovolné `cmd`, `siid/piid`, raw JSON či shell příkazy. Živé operace vybírá pojmenovaný recept s pevnou sémantikou a budgetem.
- Přidat deadline, zrušení, zámek pro jednu kameru/experiment, bounded buffer a sanitizovaný výstup. Diagnostický API vstup zůstává autentizovaný a standardně vypnutý.
- Test: dry-run provede 0 zápisů/síťových operací, cizí camera ID a špatný session generation končí před zápisem, timeout uvolní zámek, log nemá token ani raw média.

**Přejímka:** fake session zreprodukuje úspěch i chybu bez kamery a bez nekonečných retry. **Checkpoint:** bezpečně použitelný základ pro nové modely, ne kompletní debugger všech protokolů.

#### Bezpečná HTTP preflight část

`cmd/camera-probe` je první read-only krok A02. Bez `-inventory` provádí jen `GET /healthz` a autorizovaný `GET /v2/info`; `-dry-run` vypíše plán bez jediného síťového požadavku. Před skutečným requestem ověří loopback/private IP gateway, odmítne veřejný hostname/IP, URL userinfo, redirecty a nepřenáší API token v URL. Token čte pouze z předem nastaveného `XLG_API_TOKEN`. Volitelný `-inventory` spustí ještě pouze autorizovaný `GET /v2/cameras`; provider může při něm obnovit inventář Xiaomi účtu v cloudu, proto je tato část explicitní opt-in. Žádná volba nezakládá stream/session, nečte obsah snímku, neposílá PTZ ani jiný zápis.

Report neobsahuje jméno kamery, surové camera ID, IP adresu, RTSP URL, přístupové údaje ani token. Vypisuje zkrácený pseudonym camera ID, model/firmware, capabilities/stav/důvod, známá evidence a deklarovanou třídu endpointu z URL. Tato třída je pouze informace z API, nikoli pozorovaný socket/packet peer. Preflight zatím není transportní probe a neověřuje přímé spojení ke kameře; to vyžaduje další fázi A02/A03.

### A03 — Doložit a vynutit lokálnost datové cesty

**Závislost:** A02 pro plnou instrumentaci; pasivní měření existující relace může předcházet. **Rozsah:** metadata session/transportu, síťová politika, diagnostické testy a report.

1. Podle [auditu](LOCALITY-AUDIT-2026-11-12.md) rozlišit cloud účet/inventář/podpis/settings a socket s kamerou. Do privátního reportu získat peer a lokální rozhraní, do sanitizovaného jen klasifikaci, korelační ID a počty bajtů.
2. Navrhnout `local_media_required`: přímý cílový endpoint musí odpovídat ověřené kameře a povolené síti. Pouhá kontrola RFC1918 nestačí. Cloudové HTTPS pro účet zůstává samostatná politika; případný `offline_required` zakáže i toto a vrátí konkrétní nesplněný předpoklad. Žádný tichý relay fallback.
3. Doplnit negativní test pro cizí IP v `udpConn.Read`, včetně datagramu delšího než 8 B, IPv4/IPv6 reprezentací a dohodnuté změny portu při handshake. Opravit přijímání neodpovídajícího peeru bez rozbití legitimní negociace. Neoznačovat dosavadní kód za striktní LAN enforcement.
4. Živý důkaz: 2 min baseline; metadata obou stran; v izolovaném testu až 5 min WAN blokace kamery i gateway při běžící relaci. Ověřit, že blokace opravdu funguje, LAN zůstává a jde o nové snímky, ne buffer.
5. Samostatné pokusy: nový session po uzavření starého, restart pouze testovací gateway, případně řízený restart kamery. Každý má vlastní výsledek. Nepřekračovat známou platnost klíčů odhadem a nevyhlašovat trvalý offline režim z pětiminutového testu.
6. WAN pravidla pouze ve vyhrazené testovací síti/VM; připravit automatický rollback s maximální dobou a ověřením odstranění. Netestovat vypnutím celé domácí sítě. Cíle IPv4/IPv6, VPN/proxy a kamera samotná musí být zahrnuty v důkazech.

**Přejímka:** zvlášť přímý peer, warm offline, nový session offline, restart offline a settings offline. Očekávané selhání nového session kvůli cloudu je platný výsledek auditu. Pokud nelze sledovat kamerový uplink, označit hranici měření; nepředstírat kompletní síťový audit. **Checkpoint:** report a ověřená politika, nikoli tvrzení o vypnutém cloudu v samotné kameře.

### A04 — Dokončit stávající C201 cestu v Linuxu a HA

**Závislost:** A00 a naměřená cesta A03; obrazovou diagnostiku lze připravit dříve. **Rozsah:** provider/relay/HA, cílené integrační testy.

- V izolovaném HA 2026.10.4 použít zdroj z evidovaného commitu. Ověřit běžné přidání, čerstvý JPEG a 10 minut HLS z fyzické kamery, skutečné FPS/rozlišení a první snímek. H.265 ve FFmpeg není automaticky přehratelnost v klientském prohlížeči; ověřit podporovaný přehrávací formát a náklady případného převodu.
- Prokázat jeden vlastník session při současném preview, snapshotu, HA a záznamu. Definovat řízený reconnect a odlišení `offline`, `auth_required`, `privacy` a `unsupported`.
- Přenést již ověřený krokový PTZ do fyzického HA ověření jednotlivých směrů, se stejnou identitou a nejvýše jedním zápisem na akci. Tento test neopakuje hledání PTZ payloadu. Při timeoutu zabránit doručení opožděného pohybu po reconnectu.
- Zaznamenat jednu aktuální dávku šesti setting reads, je-li autorizovaný účet dostupný. Výslovně označit cloud. Neúspěch jedné vlastnosti nesmí znepřístupnit ostatní zdravé funkce kamery.

**Přejímka:** zařízení a HA důkazy vedle mock výsledků; stabilní entity po obnově. Před první hardware přejímkou neukazovat nové funkce jako všeobecně podporované. **Checkpoint:** použitelná C201 baseline pro další karty.

## 5. Ostatní funkce — samostatné karty F01–F12

Společný postup každé karty: doložit protokol → fixture/parser → provider s cancel/timeout → autentizované API → klient podle capability → cílené offline testy → omezený živý pokus → report a návrat do původního stavu. Čísla commandů/specifikace nejsou náhradou pozorovaného výsledku. Pokud funkce nemá doloženou lokální cestu, uzavři výzkumný podkrok jako `local_path_unverified` a neimplementuj náhodný zápis.

### F01 — Snímky a lokální záznam, dokončení K3

**Závislost:** A04. **Rozsah:** `internal/streamrtsp/`, `xiaomi-diagnostic-console/internal/console/media.go`, server, odpovídající testy.

- Ověřit aktuální snapshot správné kamery, čas snímku a chování při zastavené session; starý snímek označit zastaralý.
- Dokončit start/stop/list/download lokálního MKV ze stávajícího restreamu. Záznam nemá zakládat další CS2 session. Oddělit místní soubory od SD karty.
- Validovat record ID/cestu, oprávnění ke stažení, symlink/path traversal, kvótu, plný disk a nedostupný SMB share. Reconnect vytvoří jasně označený nový segment; rozpracovaný soubor se nevydává za úspěšně dokončený.
- Živě jeden přibližně 20s video-only klip do schváleného soukromého adresáře, `ffprobe`, reálné dekódování a přehrání/stažení. Kontrolovat čerstvost, délku a kodek, ne pouze příponu.

**Přejímka:** validní klip, správný zdroj, 0 dodatečných session, kontrolované selhání při plném disku. Mazání záznamů a retenci zavést jen v samostatném autorizovaném podkroku.

### F02 — Poslech zvuku z kamery

**Závislost:** A04, dostupné fyzické oprávnění pro mikrofon. **Rozsah:** stream/parser, provider, relay, capability a HA media cesta.

- Ověřit packet codec, sample rate, kanály a timestamps z reálných hlaviček; nespolehnout se na modelovou konstantu. Současný provider audio zahazuje.
- Přenést audio stejnou session, vyřešit PCMA/PCMU/Opus/PCM podle skutečného vzorku a cílového klienta. Jedna podporovaná varianta první; žádný univerzální transcoder bez potřeby.
- Oddělit požadavek na audio při startu session od jeho publikování a ukládání. Historický video pokus s `enableaudio=1` není potvrzením trvalé nutnosti ani souhlasem s nepřetržitým poslechem.
- Test: mute/default off, chybějící audio, A/V synchronizace, konec/reconnect a žádné publikování/ukládání audia při vypnutí. Živě max. 10s běžného zvuku v kontrolované scéně, měření packetů a případně poslech podle zadání.

**Přejímka:** doložený zvuk v HA, obnovitelný mute a zachované video. Při chybě jen audio unavailable, nikoli pád celého streamu.

### F03 — Kvalita videa a H.264 cesta

**Závislost:** A04; H.264 je předpoklad části starších modelů. **Rozsah:** `miss/client.go`, provider, relay a jeho testy; UI až poté.

- Změřit výstup konkrétního doloženého profilu (rozlišení, FPS, bitrate, codec). `hd`, `sd`, `auto` a číslo profilu nemají stejný význam na všech modelech.
- Nejprve read-only nabídka profilů s důkazem; potom přepnutí jedné ověřené dvojice a návrat. Neposílat cyklus 0–5 neznámé kameře.
- Rozšířit H.265-only publisher o samostatně testovanou H.264 větev: SPS/PPS/VPS podle kodeku, keyframe join, SDP, timestamps, reconnect a změna codec/rozlišení. Použít stávající transport a standardní knihovnu, nevyrábět nový RTSP server.
- Test: čekání na keyframe, poškozené parametry, opakované přepnutí bez další session a bez unbounded bufferu; skutečné HA HLS pro každý podporovaný kodek.

**Přejímka:** kvalita odpovídá změřenému obrazu a neznámý profil selže před zápisem. H.264 tvrdit až po průchodu celé publish/decode cesty.

### F04 — Noční režim, kontrolka a detekční přepínače přes skutečnou lokální cestu

**Závislost:** A01–A03. **Rozsah:** modelový profil, případný malý lokální adaptér, provider/settings API a HA.

- Výchozí známé MIoT adresy C201: `2.3` Night Shot (`0 Off / 1 On / 2 Auto`), `4.1` LED, `5.1` motion, `8.3` pet, `8.6` human, `8.7` move. Platí pro zdokumentovanou C201; nekopírovat je na jiné modely.
- První podúkol je **výzkum lokálního get**, nikoli přidání `MiotSet` do HTTP handleru. Hledat konkrétní modelový plugin, doložený lokální MIoT/miIO transport nebo dešifrovaný příkaz referenčního klienta. Samotné MIoT schéma dokládá adresy a typy, nikoli síťovou cestu.
- Pokud lokální get není doložený, ponechat nynější cloud read jako označenou možnost a zapsat blocker. Neřešit to obecným UDP RPC na kameru ani cloudovým proxy s názvem local.
- Po důkazu get vyřešit oddělený set jen pro známé writable vlastnosti: původní hodnota, validace enumu/typu, jednorázový zápis, readback a pozorovaný účinek, následná obnova původní hodnoty. Timeout zápisu = stav nejistý, nejdřív přečíst, ne opakovat.
- Night Shot: ověřit změnu scény/IR v kontrolovaném osvětlení. LED: fyzický stav. Detection enable: potvrdit konfiguraci; události řeší F08.

**Přejímka:** u každé vlastnosti get/set a její cesta zvlášť; fyzicky ověřený účinek tam, kde je požadovaný. HTTP 200 a code 0 nenahrazují účinek.

### F05 — Stav SD a kapacita

**Závislost:** A01, A02; lokální varianta také F04 výzkum. **Rozsah:** storage snapshot provider/API, modelový profil a HA read-only senzory.

- Použít [SD návrh](../xiaomi-diagnostic-console/docs/C201_SD_READONLY_DESIGN.md). MIoT `3.1–3.4` je stav, total, free, used; historické hodnoty `1/0/0/0` nemají doložené jednotky ani význam nuly.
- Uchovat raw value, per-property code, čas, `unit=null`, neznámý stav. Převod na byte/GB až podle doloženého formátu daného FW a známé testovací karty.
- Jestli čtení dál používá `MiotGetContext`, klasifikovat cloud. Rozšíření allowlistu o read-only property nesmí otevřít format/eject/action.
- Test: nulové hodnoty, chybějící property, nonzero code, overflow, unknown status; nikdy falešná informace „karta není přítomná“.

**Přejímka:** pravdivý stav a jednotky nebo explicitní unknown; žádná změna SD. Stav karty je nezávislý na seznamu klipů.

### F06 — Read-only index SD

**Závislost:** A02 a důkaz modelového storage kanálu; F05 není technická podmínka. **Rozsah:** `recordings`, `stream` RDT, optional storage provider a GET API.

- Nejdřív vzorek nebo handler, který váže C201/FW na kanál indexu. Live channel 0 není automaticky SD channel 0. Žádné skenování kandidátů.
- Provázat jeden command-6 request s odpovídající odpovědí, serializovat RDT s fetch, respektovat session generation a deadline; nezaměnit starou odpověď po reconnectu.
- Zachovat přesný epoch start, duration, event flag. UTC ukládat beze změny; timezone je až zobrazení. Stanovit limit velikosti indexu/počtu klipů a validovat strukturu.
- Rozlišit `channel_unverified` (0 sends), `available`, protokolem doložené `empty_index`, `index_timeout`, `malformed_index`, `unsupported_model`. Žádná odpověď ≠ prázdný seznam. Zdokumentovat skutečný formát prázdného indexu, jinak zůstává neověřený.

**Přejímka:** validní, prázdná a timeout fixture + jeden modelově korelovaný živý výpis. Bez doloženého kanálu skončit výzkumným výsledkem a nepřidávat fungující UI seznam.

### F07 — Výběr, stažení a přehrání SD klipu

**Závislost:** F06. **Rozsah:** storage provider/API a konzole; HA media browsing jako následný malý podúkol.

- Výběr přijímá pouze přesný start z indexu **téže** kamery, revision/snapshotu a platné relace. Navrhnout neprůhledný clip ID svázaný s camera ID + start + kanálem/profilovou revizí; nezaměnit ho za filesystem path. Zastaralý index vyžaduje refresh a nový výběr.
- U skutečně odesílaného FileCommand ověřit timestamp bytes 0–3, channel bytes 8–11 little-endian a délku 24 B (>19). Generický unit test již existuje; chybí end-to-end kontrola provider → wire a přijetí na C201.
- Fetch omezit rozpočtem bajtů/časem, podporovat zrušení klienta a přerušený soubor. Žádná neomezená RAM, libovolná cesta nebo sdílený RDT consumer bez zámku.
- Po stažení jeden krátký schválený klip dekódovat a ověřit správnou scénu/čas. Streaming playback přes `0x10D/0x10E` je **jiná větev** než stažení souboru; pokud přepíná režim živé session, explicitně obnovit původní live stream včetně chyb.
- Test: cizí camera/clip, zaokrouhlený čas, expirovaný index, odpojení, nedostatek místa a návrat live/PTZ po operaci.

**Přejímka:** jeden reálný správný klip a obnovená live session; žádné mazání nebo formátování. Přehrávání cloudového archivu není lokální SD funkce a není součástí této karty.

### F08 — Lokální události a automatizace

**Závislost:** A02/A03 a důkaz události. **Rozsah:** stream/event adaptér, provider, HA event/binary sensor, fixtures.

- Zjistit skutečný zdroj: zpráva v lokální session, lokální ONVIF event subscription nebo lokální NVR. MIoT `notify` a seznam event IID samy nedokládají odběr událostí. Cloud event stream vést jako samostatnou volitelnou cestu.
- Zachovat zdroj, device/lens ID, event type, čas zařízení i příjmu, sequence/dedupe klíč, confidence jen pokud existuje. Nastavit TTL/obnovu stavu, ošetřit duplicitní a opožděné události a nesprávný čas kamery.
- `motion_detection_enabled` zůstává konfigurace. Pro skutečný pohyb vytvořit samostatnou entitu/trigger; po ztrátě odběru nesmí zůstat natrvalo aktivní.
- První test jeden kontrolovaný pohyb → jedna událost → jedna HA automatizace, pak klid a jeden reconnect. NVR variantu testovat zvlášť, včetně nedostupného NVR a vazby na správnou kameru.

**Přejímka:** reálná událost s prokázaným zdrojem a bez zdvojení automatizace. Pokud proprietární lokální event cesta není doložena, může být nabídnuta ověřená NVR cesta s přesným označením.

### F09 — Soukromí / vypnutí obrazu

**Závislost:** doložená modelová sémantika, A04. **Rozsah:** profil, state machine, cache/snapshot/recording, HA.

- C201 `2.1 Switch Status` není dosud důkaz fyzické závěrky. Rozlišit fyzický kryt, firmware privacy, vypnutí streamu a pouze skrytí UI.
- Teprve doložená vlastnost/akce smí mít ovládací prvek. Při aktivaci zastavit publikaci obrazu, audia, záznamů a invalidovat cache; testovat závod s běžícím snapshotem/fetch a reconnectem. Nenabízet vypnutí UI jako ochranu před uploadem kamery na cloud.
- Zachovat původní stav, jednorázový pokus a návrat. Živě ověřit, zda kamera opravdu zavře čočku nebo ukončí tok podle deklarované schopnosti.

**Přejímka:** popis odpovídá fyzickému/chování firmwaru a staré snímky neunikají jako živé. Bez důkazu zůstává privacy nepodporovaná/neurčená, ne odhadovaná z ticha v síti.

### F10 — Polohy, pohybové limity a tracking

**Závislost:** A04, známé modelové příkazy. **Rozsah:** PTZ provider/profil, session dispatcher a HA tlačítka/select podle capability.

- Stávající kroky zachovat. Přidávat odděleně read position, go-to uložené pozice, správa presetů, tracking. Nezaměňovat kalibraci s polohou. Nepoužívat `operation=6/13` na C201 jen podle jiného modelu.
- Při souvislém pohybu vyžadovat skutečně ověřený stop, deadman timeout a zastavení při ztrátě klienta/session. Pokud existuje jen krok, UI zůstane krokové.
- Tracking je zápis nastavení i autonomní pohyb; původní stav a obnova. Ruční PTZ během trackingu musí mít definovanou prioritu, nikoli dvě soupeřící smyčky.
- Test: timeout po zápisu bez retry, zastaralý preset jiné kamery, ztráta session, limit pohybu; živě jedna známá poloha a návrat v bezpečné scéně.

**Přejímka:** každá podfunkce má vlastní fyzický důkaz. Podpora krokového PTZ nedává automaticky presets, zoom ani tracking.

### F11 — Mluvení do kamery

**Závislost:** F02 a modelově doložený backchannel; není blokátorem vydání video funkce. **Rozsah:** samostatný audio TX adaptér a omezené klientské UI.

- Konstanty `0x106–0x108` nejsou implementace speakeru. Zjistit formát audio negotiation, codec/rate/packet duration, transport channel, šifrování, ACK, start/stop a half/full duplex.
- Nejprve offline encoder + packetization, pak krátký řízený zvuk v běžné hlasitosti. Jediný TX klient, push-to-talk, max. délka, deadman timeout a stop při zavření stránky nebo ztrátě spojení. Mikrofon prohlížeče vyžaduje skutečnou cestu klientského oprávnění.
- Netvořit stream z dostupného mikrofonu při startu integrace. Nepřidávat sirénu jako speaker test.

**Přejímka:** zvuk skutečně slyšitelný z cílové kamery, stop spolehlivý i při odpojení, obraz přežije. Bez backchannel důkazu jen výzkumný report, žádné aktivní tlačítko.

### F12 — Ostatní modelové vlastnosti

**Závislost:** F04 postup, přesná spec a model. **Rozsah:** vždy jedna vlastnost.

Použít stejné get/set/readback testy pro flip/mirror, WDR/HDR, watermark, anti-flicker, detekční citlivost, alarm interval, illuminator nebo režim záznamu **jen pokud je daný model skutečně nabízí**. Neznámý rozsah citlivosti nenahradit univerzálním 0–100. Měnit vždy jednu položku, obnovit původní stav, kvalitu obrazu doložit porovnáním. Siréna, sirénový test a kalibrace mají odlišné účinky a nezapínají se v automatické diagnostice. Neověřené a destruktivní akce se nedostanou do obecného průvodce.

**Přejímka:** tabulka všech vlastností nalezených ve spec: implementovatelná lokálně / cloudová / neověřená / mimo aktuální rozsah, u každé důvod. „Všechny funkce“ neznamená vymýšlet podporu všeho, co Xiaomi Home někde zobrazuje.

## 6. Profily a rozšiřování modelů — M01–M07

### M01 — Profil kamery bez změny C201 chování

**Závislost:** A01/A02; připravit před druhým modelem. **Rozsah:** navržené `internal/xiaomi/profiles/`, malé integrace provideru/protokolu a fixtures.

Profil volí přesné `model ID + ověřený firmware nebo doložený rozsah + relevantní region`, transport a variantu protokolu. Oddělit tyto osy: **CS2/TUTK/RTSP** je přenos; **MISS/legacy** je aplikační formát; **H.264/H.265** je video codec. Rok výroby nebo název na krabici neurčuje ani jednu osu spolehlivě.

Návrh obsahu profilu: `profile_id`, `revision`, model/firmware matching, vendor, protocol, LAN policy, quality mapping, lens mapping, audio formats, PTZ semantics, případné storage channel + request variant, allowlist settings, známé quirks a odkazy na důkazy. Žádné per-device tokeny/IP/DID v profilu. Neznámý firmware nezíská automaticky nově rizikové funkce; zachovat bezpečnou diagnostiku a přesně označit neověřený rozsah. Profile schema musí odmítnout nejednoznačné matchování.

Runtime schopnosti jsou průnik profilu, aktuálního handshake a dostupnosti. Profil bez hardwarového důkazu je `experimental`, viditelný jen v explicitním testovacím režimu. Přidání profilu nesmí automaticky vybrat kameru z inventáře ke streamování. Stejný model dvakrát má dvě různá camera ID; více čoček má stabilní child ID pod jedním fyzickým zařízením.

**Test/přejímka:** přenesení C201 do profilu beze změny wire bytes/identity/capabilities; neznámý model, konflikt profilů, dvě kamery stejného modelu, známý model s novým firmwarem a nulové zápisy při nepodpoře. Potom teprve další profily.

### Pořadí kandidátů

Níže jsou **kandidáti**, ne seznam podporovaných zařízení tohoto projektu. První fyzický model vyber z dostupného autorizovaného inventáře; bez inventáře nenavrhuj nákup konkrétní kamery jako podmínku práce.

| Vlna | Kandidát / vlastnost | Práce | Brána |
| --- | --- | --- | --- |
| M02 | Jednočočkový CS2+MISS; např. C200 `chuangmi.camera.046c04`, 2K `chuangmi.camera.029a02` | Znovupoužít session, ověřit profil/codec/PTZ samostatně | Skutečný model/FW a první snímek; ne kopie C201 MIoT adres |
| M03 | Starší TUTK+MISS; např. `chuangmi.camera.v2` s odpovídajícím FW | Přidat transport, zachovat MISS parser jen tam, kde odpovídá | Doložený handshake a přímý peer, bez tichého relay |
| M04 | Legacy/TUTK; např. `chuangmi.camera.ipc019e`, Xiaofang `isa.camera.isc5`, konkrétní Dafang `isa.camera.df3` | Samostatný legacy codec/auth/command adaptér | Přesná firmware varianta; H.264 cesta z F03 |
| M05 | Dvě čočky / hybridní zoom | Sdílená fyzická session, demux a child identita | Dva skutečné obrazy a korektní ovládání příslušné čočky |
| M06 | Bateriové kamery, zvonky nebo kamera za base station | Wake/sleep, proxy základna, event-first provoz | Doložená cesta a spotřeba; žádný permanentní live polling |
| M07 | Jiný vendor (Agora/MTP), neznámý protokol nebo nativní RTSP/ONVIF | Nejprve klasifikace a samostatný adaptér | Žádný fallback na CS2 podle obchodního názvu |

Upstream [seznam go2rtc](https://github.com/AlexxIT/go2rtc/issues/1982) uvádí výše uvedené rodiny a u některých modelů rozdíly podle firmwaru. Je to vodítko k experimentu, nikoli naše přejímka. Zvlášť u `chuangmi.camera.v2` a Dafang je důvod evidovat firmware. Nepřebírat tvrzení „před/po roce 2020“ jako rozhodovací algoritmus.

### M02 — Druhý CS2 model

- **Rozsah:** jeden nový profil, fixture a minimální modelový quirk. Žádné plošné povolení všech `*.camera.*`.
- Projít diagnostické stupně D0–D5 z oddílu 7. Ověřit 10 minut videa, snapshot, session recovery, lokální trasu, skutečný codec/profil; PTZ/audio jen zvlášť, pokud pro ně existuje opora.
- Přesnou MIoT spec stáhnout/načíst jako podklad daného modelu, ne přebrat SIID z C201. Pokud se hlásí jiný vendor, přesměrovat práci do M03/M07.
- Testovat C201 i nový model současně a všechny předchozí UI identity regrese.

**Přejímka:** konkrétní model/FW/region ve kompatibilitní matici s per-feature důkazy a bez změny výstupu C201. Bez fyzického zařízení výsledek pouze `hardware_pending`.

### M03 — TUTK s MISS

- **Rozsah:** samostatný transport adaptér a interface na existující MISS klient. TUTK nyní není v tomto repozitáři implementováno.
- Nejdřív porovnat připnuté [go2rtc MISS zdroje v1.9.14](https://github.com/AlexxIT/go2rtc/tree/v1.9.14/pkg/xiaomi/miss) a jejich licence/dependencies; zapsat přesné soubory a commit. Upstream tag je výchozí podklad, ne povinnost stáhnout nový SDK.
- Ověřit direct LAN discovery/auth a schopnost skutečně vyloučit relay. TUTK může mít vlastní NAT/P2P/relay logiku; bez měřitelného peeru/zákazu fallbacku nepoužít označení local-only.
- Pokud je nutné vendor SDK, samostatně doložit licenci pro redistribuci, amd64/aarch64 buildy a životní cyklus. Preferovat malý volitelný adaptér; nepřibalovat binárku s nejasnými právy.
- Test: fake TUTK read/write/close, timeouts, chybné délky/pořadí, nepodporovaná SDK varianta, současný CS2 profil a nedostupná volitelná závislost. Živě nejprve pouze video.

**Přejímka:** MISS video přes ověřený TUTK direct path a izolované selhání adaptéru; ostatní ovládání zatím vypnuté.

### M04 — Starší legacy modely včetně Dafang/Xiaofang

- **Rozsah:** samostatný protocol adaptér a jeden profil/FW na úkol. [go2rtc legacy v1.9.14](https://github.com/AlexxIT/go2rtc/tree/v1.9.14/pkg/xiaomi/legacy) je kandidát pro zdrojové srovnání. Nezaměňovat tento Xiaomi legacy protokol s naším statickým `/v1` API.
- Odlišit starý auth/key derivation, TUTK framing, media headers, timestamps, video/audio varianty a specifické ICAM příkazy. H.264 a případné PCM ověřit ve F03/F02 cestě.
- Model s různými firmware větvemi může vyžadovat MISS nebo legacy; matching musí mít důkaz. Nezkoušet náhodně oba zápisové protokoly. Nejprve autorizovaný read-only handshake a header fixture.
- Bez upgrade/downgrade firmwaru a bez odstranění bezpečnostních kontrol pro starý model. Nový firmware = nová zkouška, nikoli automatický rollback zařízení.

**Přejímka:** konkrétní starší model/FW má aktuální video v HA a obnovu. PTZ se přidává až s vlastním doloženým wire formátem a fyzickým testem.

### M05 — Více čoček a zoom

- **Rozsah:** profily, `stream/shared.go`, demux, kamera/stream identita a UI.
- V protokolovém kódu již jsou dual-lens helpery převzaté z upstreamu. Jejich komentáře o CW500 nejsou naše hardware ověření a provider je obecně nezpřístupňuje.
- Každou čočku samostatně spojit s packet tagem a scénou, potom zapnout obě. Ověřit nezávislý start/stop konzumentů a jeden physical owner. Vždy mít limit počtu streamů.
- Hybridní zoom není důkaz dvou nezávisle dostupných streamů. Nezaměňovat varianty CW500 se stejným obchodním jménem. PTZ vážeš k fyzickému mechanismu; klient musí vidět, kterou čočku ovládá.

**Přejímka:** dva skutečné odlišné obrazy bez záměny A/B, správné child ID, single-session ownership a stabilní C201 regrese.

### M06 — Baterie, zvonky a základnové stanice

- **Rozsah:** profil napájení/wake, event adaptér a session policy; nenasazovat trvalý stream automaticky.
- Rozlišit IP kamery a base station, kde probíhá autentizace, přenos a ukládání. Cloud `WakeUp` helper není automaticky lokální probuzení.
- Omezit frekvenci wake, dobu session a polling; určit TTL cache a stáří stavu. Offline/spící zařízení nesmí spouštět nekonečné reconnecty nebo vypadat jako chybný účet.
- Před delším testem změřit baseline baterie/počtu probuzení a zachovat běžnou funkci zvonku. Event-trigger a návrat do spánku mají vlastní evidence.

**Přejímka:** read-only stav, kontrolované krátké video, korektní spánek a doložená cesta přes základnu. Bez vzorku a hardware zůstává výzkumná větev otevřená.

### M07 — Jiný vendor a standardní RTSP/ONVIF

- Pro Agora/MTP nejprve zjistit existenci použitelné lokální cesty, SDK/licenci a měřitelnost peeru; pokud není doložená, vrátit `unsupported_transport`. Překlad čísel vendor v cloud klientu není jeho implementace.
- Kamera s nativním RTSP/ONVIF má použít existující statický provider. Ověřit přesné streamy/profily a PTZ capability, snapshot a auth. To rozšiřuje kompatibilitu bez nutnosti přenášet Xiaomi session protokol na standardní zařízení.
- Bez důkazu nepovolovat ONVIF jen proto, že port odpoví, a nerozšiřovat povolené RTSP adresy na libovolné cíle. Uchovat existující ochranu proti úniku credentials a nevhodným cílovým adresám.

**Přejímka:** každý adaptér má samostatný kontrakt, testovací fixture a izolované selhání; společné entity nezávisí na volitelném SDK.

## 7. Mechanismus ladění nové či nepodporované kamery

Tato specifikace je hlavní opakovatelný postup pro A02 a M02–M07. Nevytvářet pro každý model další anonymní skript v `.test-env` bez zdrojů a výsledků.

### D0–D6: diagnostický automat

| Stupeň | Vstup a operace | Povinný výstup / kdy skončit |
| --- | --- | --- |
| D0 Identita | Povolený inventář, přesný model/FW/region, napájení, soukromý device binding | Anonymní label + profil candidate; neznámá identita = žádný síťový příkaz na odhadnutý cíl |
| D1 Síť a účet | Trasa, čas, oprávnění procesu, stav autorizace, známý endpoint | Oddělený `environment_error`, `auth_required`, `device_unreachable`; žádné automatické login kolečko |
| D2 Transport | Jedna doložená metoda discovery/handshake, peer a vendor | `cs2/tutk/...`, TCP/UDP, local/unknown peer; konec při neznámém protokolu |
| D3 Media | Doložený start profilu, headers, first packet, codec params/keyframe, první decode | Přesná poslední dosažená fáze; 0 decoded frames ≠ 0 packets |
| D4 Řízení | Nejprve doložený read-only command/reply, pak jednotlivé autorizované akce | `not_sent / write_ok / reply_received / effect_verified`; ACK není účinek |
| D5 Stabilita | 10 min, odpojení testovacího klienta, jeden řízený reconnect, lokálnost A03 | Čerstvé snímky, latency, reason ukončení, počet session; bez úniku gorutin/FD |
| D6 Zařazení | Profil + fixture + provider/API + skutečné HA entity | Záznam kompatibility s důkazy, případné hardware_pending; default capability pouze podle prokázaného rozsahu |

**Rozpočet jednoho diagnostického běhu:** výchozí celkový deadline 90 s včetně cloudových fází, nejvýše jeden aktivní experiment na zařízení a jeden předem známý profil. Delší endurance/SD transfer je explicitní jiný recept s vlastními limity. Zápisový experiment má nejvýše jeden zápis před vyhodnocením; návrat původního nastavení je samostatně zaznamenaná operace. Sběr metadat po celou dobu, obsah snímků/audia jen je-li součástí autorizovaného pokusu.

### Navržené ovládání runneru

Budoucí CLI přijímá pouze lokální camera alias/opaque ID, recept a cestu privátního konfiguračního souboru; secrets nikdy v argumentech. Příklady **dosud neexistujících** receptů: `preflight`, `session-metadata`, `video-first-frame`, `settings-read`, `sd-index`, `ptz-single-step`. `--dry-run` vypíše plán a nulové síťové operace. Recept může vyžadovat potvrzení existujícího oprávnění z pracovního zadání, nikoli vynucovat nový dialog při každém už autorizovaném kliknutí.

Každý recept je v kódu explicitní allowlist: vstupy, modelový profil, očekávané command/reply, timeout, max. bytes, vliv na session a cleanup. Arbitrární `SendRaw` zůstane interní vývojová pomůcka; nedostane se přes URL do prohlížeče. Neznámé wire vzorky se zpracují offline.

### Výstup běhu a redakce

Navržené uložení: `.test-env/camera-probes/<run-id>/private/` pro místní citlivé podklady, `evidence/` pro sanitizované výsledky. Do Git jen cíleně zkontrolované fixtures, report a profil; žádné celé exporty účtu, `.storage`, PCAPy nebo videa.

```json
{
  "run_id": "<id>",
  "source_commit": "<sha>",
  "executable_sha256": "<sha>",
  "camera_alias": "test-camera-a",
  "model": "<exact model>",
  "firmware": "<exact version>",
  "region": "<region>",
  "profile_revision": "<id>",
  "recipe": "video-first-frame",
  "stage": "decode",
  "result": "pass|fail|blocked|inconclusive",
  "transport": "cs2+tcp",
  "peer_class": "enrolled_camera_lan|cloud|unknown",
  "cloud_operations": ["session_negotiate"],
  "media_packets": 0,
  "decoded_frames": 0,
  "first_frame_ms": null,
  "writes_attempted": 0,
  "writes_confirmed": 0,
  "reply_received": false,
  "effect_verified": false,
  "cleanup_result": "<result>",
  "evidence_refs": [],
  "next_single_experiment": "<one concrete step>"
}
```

Jde o šablonu, nikoli naměřené nuly či tvrzení o výsledku. Fáze, které vůbec nenastaly, mají `null/not_run`, ne nulu. V reálném reportu přidat UTC časy a monotónní duration, session generation, counts lost/dropped/unknown channels, anonymní operation ID a error class. Peer/socket metadata v privátním manifestu propojit se sanitizovaným aliasem.

Logovat pouze allowlist decoded polí; auth response může obsahovat tajné údaje i při chybě. Konkrétní nález pro kontrolu: `miss.login` dnes umí vrátit tělo odmítnuté autentizace v error textu. Runner nesmí bez redakce kopírovat celý `error.Error()` ani `LastReply`. Filtr otestovat fixtures se záměrně vloženými fake secrets.

### Reprodukce neznámého problému

1. Zapsat jednu hypotézu a očekávané odlišující pozorování. Nejprve zjistit, zda problém vzniká v cloudu, discovery, auth, video headeru, dekodéru nebo klientovi.
2. Použít známý funkční klient stejného modelu/FW jako referenci, pokud je oprávněně dostupný. Zachytit jen nutná metadata. Šifrovaný PCAP sám neodhaluje plaintext příkaz; přednost má zkontrolovaný parser/handler nebo diagnostika referenčního klienta.
3. Offline fixture zachová framing/length/order a odstraní klíče, identifikátory a osobní média. Syntetický vzorek označit synthetic; neříkat mu hardware capture. Uvést původ/licenci/checksum.
4. Nejprve reprodukovat chybu v testu. Jedna malá oprava parseru/adaptéru, bez kopírování celého upstream main. Fuzzování jen offline, bounded vstupy a žádné skutečné sockety.
5. Jediný živý potvrzovací běh stejné hypotézy. Po stejném selhání podruhé vypsat poslední úspěšnou fázi a co chybí k dalšímu postupu; nezkoušet okolní command IDs.
6. Před přidáním podpory projít D5/D6. Zaznamenat i známé nepodporované funkce a firmware varianty, aby další model neopakoval slepé pokusy.

## 8. Testovací matice, provoz a vydání

### Příkazy, které již existují

V kořeni gateway, na kompatibilním Linuxu:

```sh
go test -race ./...
go vet ./...
go build ./cmd/gateway
```

V `xiaomi-diagnostic-console` jako samostatném Go modulu:

```sh
go test ./... -count=1
node scripts/Test-Preview-UI.js
```

Na Windows s funkčním WSLc spustit z rootu `./scripts/test-local.ps1`; `-ExerciseMockPtz` je pouze simulace. Pinned HA 2026.10.4 a image digest přebírat ze skriptu, ne vymýšlet novou verzi. V HA runtime spustit existující `tests/test_gateway_client.py` a `tests/test_camera_settings.py`; kompletní runner navíc provede setup a E2E. Lokální Windows Go je `C:\Apps\go\bin\go.exe`; není náhradou Linux testů POSIX store ani HA. Povinné testy s timeoutem/sockety nesmí být označeny passed po jejich přeskočení.

### Minimální pokrytí změn

| Změna | Povinný důkaz |
| --- | --- |
| Parser / transport | known-good fixture, zkrácený/poškozený input, wrong peer, deadline/cancel, EOF a souběh |
| Modelový profil | přesná identita/FW, konfliktní match, unknown model, C201 regrese, 0 writes při odmítnutí |
| API | auth, model capability, typ/rozsah vstupu, cizí ID, timeout, bez secret leakage |
| Stav / cache | pozdní odpověď, reconnect generation, dvě kamery, stale data a odhlášení |
| HA platforma | skutečný ConfigEntry `loaded`, registry ownership, správné states/unavailable, unload/reload a žádné dvojí entity |
| UI | výběr A/B, refresh bez selection, opožděný polling, 1 klik/1 zápis, mock 0 gateway calls |
| Hardware | model/FW/build, příkaz/odpověď/účinek zvlášť, peer + cloud závislosti, recovery |

### R01 — Dlouhodobá stabilita a běžný instalační tok

**Závislost:** přijaté schopnosti, A00/A04 a první další profil podle rozsahu vydání. **Rozsah:** runtime packaging a integrační opravy, nikoli další protokolový výzkum.

- Dokončit stávající T05–T09: účet/region, bezpečné párování, persistenci, reauth a výběr přes běžný HA průvodce; autentizované dosažitelné API i média mezi kontejnery. Nepublikovat do LAN credential-free RTSP bez vyřešené ochrany a nepovažovat loopback jiného kontejneru za HA adresu.
- HA OS a Container mají společný backend, odlišný instalační tok. Ověřit amd64/aarch64 jen pokud je platforma slíbená a má všechny závislosti. Prohlížeč/CLI v diagnostice není náhradou čisté instalace HA.
- 10 řízených cyklů výpadku/obnovy a 72h soak v určeném testovacím prostředí. Původní cíle: první snímek do 15 s, oznámení výpadku do 30 s, obnova do 120 s na zdravé referenční síti. Uvést měření, ne předpoklad.
- Kontrolovat růst RAM/gorutin/FD, počet session, fronty a zahozené snímky; jedna offline kamera nesmí zastavit ostatní. Změna výběru v jedné HA instanci nesmí ovládat jinou kameru omylem.
- Build manifest svázat s commitem a launcherem. Běžný CMD/supervised start musí přes HTTP servírovat aktuální JS/API; zamčený historický EXE se řeší verzovaným výstupem a explicitním přepnutím, ne ukončením cizího procesu.
- Ověřit aktualizaci a návrat na předchozí release se zachováním dat/identity. Změna schema store má migrační test a rollback; nesmazat nečitelný store pro dosažení zeleného testu.

**Přejímka:** aktuální kompatibilitní matice, install/upgrade/recovery důkazy a žádné nepravdivé tvrzení o lokálnosti nebo podpoře. Funkce s `hardware_pending` nesmí být uvedena jako ověřená.

## 9. Řízení postupu a předání dalšího kroku

Při zahájení implementace vytvoř **nový** `docs/LOCAL-CAMERA-PROGRESS.md`. Tento plán nepřepisuj po každém testu. U každé karty eviduj:

| Pole | Obsah |
| --- | --- |
| Task / dependency | Např. A00; splněné předpoklady nebo otevřený blocker |
| Source / build | Commit, dirty hash pokud nutný, executable/image digest |
| Scope | Konkrétní měněné soubory a jedna výsledná funkce |
| Evidence | Příkaz, prostředí, datum, exit code, výsledek, cesta logu |
| Validation | Offline / physical / HA / LAN proof samostatně |
| Known limits | Co není potvrzené, na kterém modelu/FW/regionu |
| Next task | Právě jedno ID; případně přesný externí předpoklad |

První fronta: **A00 → A01 → A02 → A03 → A04 → F01**. Při čekání na hardware lze pracovat na offline A01/A02/M01; nesmí se tím uzavřít živá přejímka. Po F01 pokračovat F02/F03; F04–F07 jen po svých důkazních branách. M01 musí předcházet druhému profilu; M02 je první rozšíření kompatibility. F08–F12 a M03–M07 se berou po jedné podle dostupného hardwaru. Chybějící zařízení musí být explicitní blocker, ne důvod rozšířit allowlist naslepo.

### Připravený zahajovací prompt pro levný model

```text
Pracuj v C:\DEV\xiaomi-local-gateway. Toto je nové zadání IMPLEMENTACE.
Čti docs/LOCAL-CAMERA-HANDOFF.md, docs/LOCALITY-AUDIT-2026-11-12.md
a případný docs/LOCAL-CAMERA-PROGRESS.md. Výchozí checkpoint je 3cb89e8;
zachovej novější změny uživatele a nejprve ověř skutečný HEAD.

Proveď právě úkol A00. Požadovaný výsledek je skutečný headless Go + HA
2026.10.4 běh včetně late-settings se skutečným loaded ConfigEntry a přesnými
entitami. Použij existující runner; při nedostupném WSLc prověř jeden dostupný
Linux runtime. Neopakuj stejný E_ACCESSDENIED bez změněného předpokladu.
Neoslabuj testy, neměň funkce kamer ani neotvírej fyzickou session v této kartě.

Pokud nelze testy spustit, zapiš konkrétní blocker a připravený postup pro Linux;
nevydávej syntaktickou kontrolu za HA test. Na konci uveď změny, příkazy,
výsledky a právě jeden další úkol. Další karty automaticky neimplementuj.
```

Pro pozdější karty nahraď jen ID a výsledek příslušnou kartou; drž pracovní smlouvu. Pokud uživatel výslovně zadá celou posloupnost, pokračuj po checkpoints autonomně, ale závislé živé kroky nepřeskakují důkazní ani autorizační brány. Ukončení výzkumu s `unverified` není splnění požadavku „funkce funguje“.

## 10. Hranice tohoto předání

Tímto zadáním vznikly pouze tento plán a audit lokálnosti. Žádná nová funkce, live stream, MIoT zápis, WAN blokace, benchmark, modelový profil nebo testovací runner nebyly implementovány. Oprávnění a výsledky předchozích živých testů zůstávají omezené svým rozsahem; nejsou plošnou přejímkou budoucích modelů.
