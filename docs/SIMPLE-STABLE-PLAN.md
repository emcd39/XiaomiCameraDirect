# Jednoduché a stabilní přidání Xiaomi kamer

Stav plánu: postupná implementace běží, aktualizováno 6. 11. 2026. Přesné hotové části, důkazy a otevřené brány jsou v [IMPLEMENTATION-STATUS.md](IMPLEMENTATION-STATUS.md); mock průchod neuzavírá fyzickou podporu C201.

## 1. Výsledek pro uživatele

Po instalaci produktu uživatel v Home Assistantu otevře **Přidat integraci → Xiaomi Local Gateway → Připojit Xiaomi účet → Region → Vybrat kamery → Ověřit obraz → Dokončit**. Konkrétní pořadí regionu a přihlášení respektuje skutečný přihlašovací protokol.

- Běžný průvodce nežádá adresu bridge, API token, RTSP URL, ONVIF profil ani úpravu YAML.
- Přihlášení může otevřít stránku Xiaomi; po návratu pokračuje tentýž průvodce. Nevyžaduje kopírování tokenů z vývojářských nástrojů.
- Po restartu HA, balíčku, kamery nebo routeru se nastavení zachová a spojení se samo obnoví, pokud platí autorizace a síť je dostupná.
- Při problému se zobrazí příčina a konkrétní další krok. Úspěšná odpověď gateway sama o sobě neznamená funkční obraz.
- Každá kamera má samostatně ověřené schopnosti. Video nesmí čekat na podporu PTZ či Frigate.
- C201 je první fyzický ověřovací model. Podporu nelze slíbit před testem konkrétního model ID, firmwaru a regionu.

Zařízení musí být nejprve spárované v Xiaomi Home. Zůstává vysvětlené omezení: Xiaomi cloud může být nutný pro přihlášení a navázání nové relace; živé video má používat LAN. Neslibovat fungování nových relací při nedostupnosti cloudu.

## 2. Výchozí stav a mezery v kódu

Stav pracovního stromu a posledního izolovaného testu:

| Oblast | Současný stav | Potřebná změna |
| --- | --- | --- |
| `config_flow.py` | URL gateway a ručně vložený token, identita podle URL | Spravované připojení, přihlášení, výběr kamer, stabilní identita, reauth |
| `internal/config/config.go` | Legacy JSON vyžaduje nejméně jednu statickou RTSP kameru; explicitní `-managed` startup tuto konfiguraci obchází a používá managed store | Xiaomi inventář a vazba jeho stabilních ID na managed store |
| `internal/httpapi/api.go` | `/v2/cameras` přes statický RTSP/ONVIF provider; managed `/v2/info` a revision-checked `/v2/selection` používají trvalý store; `/v1` zachováno | Dynamický Xiaomi inventář, účet a autorizovaná managed média |
| `entity.py` | Dostupnost podle inventáře a dosažitelnosti API | Samostatná dostupnost videa a ovládání podle stavu kamery |
| `camera.py` | Předaná RTSP adresa; snapshot vrací `None` | Ověřený obraz a snapshot, zachování entity při výpadku |
| `coordinator.py` | Polling inventáře po 30 s, všechny chyby jako `UpdateFailed` | Rozlišení dočasné chyby, chyby gateway tokenu a Xiaomi autorizace |
| `Dockerfile` / Compose | Gateway image má `/data` volume a unmanaged výchozí start; externí RTSP/ONVIF nutné | Managed backend/runtime, přetrvávající deployment volume a instalační příklady |
| CI | Go race/vet/build, Python gateway-client behavior a kompilace HA integrace | Zbývá managed login/store/media testování a fyzická obnova relace |

Aktuální izolovaný HA Container 2026.10.4 běh je `.test-env/evidence/test-local-20261106-121819-f540d7c5.log`; runner vytvořil nový unikátní stack a odstranil ho po kontrole. Go race/vet včetně T03 persistentního selection API a legacy/v2 dekodérových regresí, sedm Python testů klienta, HA `check_config`, config flow, entity/capability assertions, čtyři mock PTZ směry a obnova po restartu prošly. **Neprokazuje skutečné RTSP video ani podporu C201.** Starší `.test-env/integration-test-report.md` není živý stav ani zdroj aktuálních výsledků.

Pracovní strom již obsahuje rozpracované změny v API, config flow, ONVIF, testech a `.gitignore`. Zachovat je; nezačínat resetem ani jejich přepsáním.

## 3. Rozhodnutí o podobě produktu

1. **Primární uživatelská instalace je HA OS:** jedna integrace a jeden náš doplněk (v novější dokumentaci HA „App“). Doplněk obsahuje gateway a vybraný kamerový backend. Instalace integrace a doplňku jsou dva technické artefakty; veškerá běžná správa účtu a kamer probíhá v jednom průvodci integrace.
2. **HA Container / zdejší WSLc:** stejný runtime image jako jedna námi připravená služba s jedním datovým svazkem. HA Container nemá Supervisor; průvodce nesmí nabízet instalaci doplňku přes neexistující Supervisor. Jednorázové spuštění služby je součást instalačního postupu, další správa je v HA.
3. **HACS instaluje integraci.** Neplánovat, že sám poskytne Supervisor nebo bezpečně nainstaluje libovolný doplněk. V HA OS nabídnout doložený postup instalace doplňku a návrat do průvodce; automatizovat jen skutečně ověřená API. Bezpečné párování bez ručního tokenu je povinná součást T06.
4. **Nejprve znovupoužít funkční backend.** Výchozí kandidát je připnutá verze `xiaomi-onvif` / patched go2rtc, kterou již předpokládá M0. Konkrétní revizi vybírá T01 podle C201, licence a možnosti programového přihlášení. Další kandidát je vendor SDK cesta používaná `ha-xiaomi-camera`; její dostupnost, licence, regiony i schopnosti se musí samostatně ověřit. Neinstalovat oba backendy do první verze.
5. **Vlastní MISS/CS2 implementaci odložit.** Jednoduché ovládání nevyžaduje přepsání transportu. Pokud T01 neprokáže použitelnou cestu pro C201, zdokumentovat konkrétní chybějící část a samostatný omezený průzkum. Nepředstírat podporu přidáním názvu modelu do allowlistu.
6. **Jeden vlastník relace:** backend vlastní jednu kamerovou relaci pro vybrané zařízení; HA i Frigate odebírají lokální restream. Session manager řídí požadovaný stav a obnovu, nesmí soupeřit s vlastním reconnect loopem backendu.
7. **První verze: jeden účet a region na gateway.** Další účty jsou rozšíření. Interní identita počítá s účtem/regionem/device ID a nespoléhá na IP, název nebo pořadí kamery.
8. **Zachovat režim stávající RTSP/ONVIF gateway** jako pokročilou volbu. Statická konfigurace má dosavadní validaci; prázdný inventář je povolen jen v novém managed režimu. Stávajícím entitám neměnit ID bez prokázané migrace.

```text
Home Assistant: průvodce, kamery, diagnostika, opravy
                 | autorizované lokální API
Jeden náš runtime / doplněk
  gateway + trvalé nastavení + správce relací
                  |
  připnutý backend -> lokální restream -> HA / volitelně Frigate
                  |
               Xiaomi kamera v LAN
```

Backend může být samostatný proces uvnitř balíčku. Uživatel nemá ručně instalovat, přihlašovat ani udržovat další bridge. Supervisor doplněk i Container varianta musí řešit skutečné adresy mezi procesy: `127.0.0.1` v jednom kontejneru není automaticky HA, jiný kontejner ani hostitel.

## 4. Společné smlouvy před implementací UI

T02 smlouva je v `docs/API-MANAGED.md` včetně příkladu odpovědi, chyb, lifecycle identit a omezení stream authorization. Níže uvedené endpointy, které nejsou výslovně označené implementované, jsou **návrh pro nové API**.

- Zachovat dosavadní `/v1/cameras` a PTZ endpointy pro staré klienty. Nové managed operace dát do `/v2/`; vyjednat verzi a schopnosti přes autorizované `GET /v2/info` (`instance_id`, `api_version`, `features`).
- Nastavení a restart uchovávají stabilní `instance_id`. Obnovení zálohy zachová identitu; klon pro novou instalaci ji výslovně změní. Discovery předává adresu a identitu, nikdy tajný token.
- Přihlášení: `POST /v2/auth/flows`, `GET /v2/auth/flows/{id}`, `DELETE /v2/auth/flows/{id}`. Stavy `waiting`, `action_required`, `complete`, `expired`, `cancelled`, `error`; konkrétní Xiaomi login/redirect vychází z T01. Jednorázový nonce/state, omezená platnost, vazba na párovaného klienta, žádná libovolná callback URL. Výsledkem není zveřejněný Xiaomi token.
- Inventář účtu `GET /v2/devices`; výběr celého seznamu idempotentně přes `PUT /v2/selection` s revizí a konfliktem při souběžné změně. Trvalé výběry jsou zdrojem pravdy i po restartu.
- `GET /v2/cameras` poskytuje stabilní ID, model, capability flags, `selected`, `state`, `reason`, `last_frame_at`, čas poslední změny a stáří měření. Oddělit stav videa, ovládání a účtu. Nevyzrazovat UID účtu ani device secrets v běžných HA atributech.
- Stavy videa: `not_selected`, `unsupported`, `idle`, `connecting`, `streaming`, `retrying`, `offline`, `auth_required`, `privacy`, `error`. `privacy` se používá jen při doložené informaci z kamery; ticho v síti není důkaz vypnuté čočky. `idle` znamená bez aktivního požadavku na video, nikoli ověřený přenos.
- Snímek / ověření obrazu používá omezený request přes existující relaci, nikoli nové paralelní připojení ke kameře. Starý snímek má čas pořízení a nesmí se vydávat za aktuální.
- UI chyby mají stabilní kód a přeloženou zprávu: `wrong_region_or_empty_account`, `auth_required`, `unsupported_model`, `unsupported_transport`, `lan_unreachable`, `stream_timeout`, `codec_unsupported`, `backend_unavailable`, `storage_error`, `api_incompatible`. Prázdný seznam sám o sobě nedokazuje chybný region.
- Každá operace má timeout, schéma, omezenou velikost odpovědi a pravidla pro opakování. Žádné automatické opakování PTZ příkazu. Účet čekající na reauth zastaví nové cloud login pokusy; existující funkční relace zbytečně nerušit.
- `/healthz` zůstává liveness, `/readyz` nepředstírá online kamery. Pro připravenost služeb doplnit oddělený stav backendu. Offline kamera sama nespouští nekonečné restartování celého balíčku.

T02 kontrakt odlišuje API authorization od samotného RTSP přístupu; bezpečný přenos médií se musí prokázat v T06 před napojením managed UI. Neřešit důvěru tím, že discovery automaticky přidělí správcovský token.

## 5. Implementační úkoly pro levný model

Postupovat po jednom úkolu; větší úkol rozdělit na malé funkční změny podle přiložených dílčích bodů. Každý končí reportem `hotovo / testováno / neověřeno / další krok`. Označení hotovo vyžaduje důkaz pro uvedené podmínky. Simulace neuzavírá fyzické ověření.

### T00 — Reprodukovat výchozí testy

- Závislost: žádná. Číst tento plán, `README.md`, současný kód a lokální test report.
- Výstup: `scripts/test-local.ps1`, sanitizované test fixtures a přenosné testy pod `tests/`; vyjít z `.test-env`, nekopírovat účet, `.storage`, databázi, videa ani env soubory. Runtime credentials zůstávají mimo Git.
- Nejdříve pouze ověřit kontejnery přes WSLc. Existující capture skript zastavuje gateway a fyzicky by odesílal PTZ; nespouštět ho na jiných cílech než pojmenovaném mock stacku.
- Přenést již existující negativní Python testy do běžných testů. Zachytit výchozí Go testy, HA setup a mock obnovu; CI připnout na skutečně použitou testovanou verzi HA/Python.
- Hotovo: příkazy a exit kódy lze zopakovat bez místních absolutních cest a bez tajných údajů ve výstupu. Nedostupné WSLc označit jako omezení prostředí, nikoli úspěšný test.

### T01 — Prokázat C201 a vybrat backend

- Závislost: T00 pro reprodukovatelné prostředí; průzkum zdrojů lze dělat dřív.
- Výstup: `docs/COMPATIBILITY.md`, `docs/ADR-002-MANAGED-BACKEND.md`, malý sanitizovaný probe report. Žádný velký refaktor.
- Získat skutečné model ID, firmware a region testované C201 z informací o zařízení nebo inventáře účtu. Obchodní název nestačí; nezaměňovat C201 a C200. Heslo a token se zadávají lokálně do přihlašovacího toku, ne do chatu ani fixture.
- Porovnat nejvýše dva kandidáty z oddílu 3. Zapsat repozitář, konkrétní commit/tag a checksum/digest, platformy, licenci včetně binárních závislostí, dostupný login flow, LAN režim a možnost ovládání bez ruční editace souborů. README není doklad funkční C201.
- Izolovaně ověřit přihlášení, inventář, první skutečně dekódovaný snímek, deset minut obrazu a jedno obnovení relace. PTZ ověřovat odděleně a pod dohledem uživatele; není podmínkou úspěchu video pilotu.
- Protokol přebírat z výsledku negociace/logů, ne odhadovat podle jména modelu. Ověřit, zda lze zakázat cloud relay; nedoloženou LAN-only cestu označit jako neověřenou.
- Hotovo: zvolený kandidát splní video pilot a je programově použitelný, nebo vznikne přesný blocker s posledním ověřeným krokem. Bez přístupu k zařízení zůstává výsledek `hardware_pending`.
- Rozhodovací brána: T02–T03 a HA mocky mohou pokračovat nezávisle. Produkční backend, finální login a tvrzení o podpoře C201 čekají na doložený výběr. Po neúspěchu obou kandidátů nenahrazovat výsledek odhadem; další práce je samostatný průzkum protokolu.

### T02 — API, identity a rozhraní backendu

- Závislost: T00; backendově specifické části po T01.
- Výstup: `docs/API-MANAGED.md`, malé typy a rozhraní v `internal/backend/`, `internal/model/`, kontraktové fixtures a testy.
- Rozdělit podle schopností: inventory/auth, media/status, volitelné PTZ. Jeden obří interface nesmí nutit video-only backend implementovat falešné ovládání.
- Zapsat všechny smlouvy z oddílu 4, způsob stream authentication a mapování ID původních kamer. Zachovat ID stávajících entit, neodvozovat nové identity z URL.
- Hotovo: fake backend lze vložit do API a simulovat prázdný inventář, offline kameru, nepodporované PTZ a auth expiry. Test stávajícího `/v1` klienta stále prochází. UI a Go kód používají tutéž kontraktovou fixture.

### T03 — Trvalé nastavení a prázdné první spuštění

- Závislost: T02.
- Výstup: `internal/store/`, managed režim v `internal/config/`, testy a migrace datového schématu.
- Nejprve stabilní instance ID a výběr kamer; následně oddělené úložiště autorizace podle T01. Runtime `/data`, atomický zápis přes dočasný soubor/rename, restriktivní oprávnění, validace a záloha před migrací.
- Managed režim startuje bez kamer a bez připojeného Xiaomi účtu. Legacy režim nadále odmítá prázdnou konfiguraci a neplatné RTSP/ONVIF.
- Pád v průběhu zápisu nezničí předchozí platný stav. Poškozené úložiště se nesmí tiše nahradit novou identitou nebo prázdným účtem. Jedna runtime instance má výhradní vlastnictví svého datového svazku.
- Hotovo: restart zachová identitu/výběr, duplicitní selection nevytvoří duplicity, konflikt revize je odmítnut a neúspěšná migrace ponechá použitelnou zálohu.

### T04 — Zabalit jeden skutečný backend

- Závislost: T01–T03. Rozdělit na adaptér a následně image/životní cyklus procesů.
- Výstup: `internal/backend/<vybrany_backend>/`, připnutý build, `THIRD_PARTY_NOTICES.md`, checksumy a úprava `Dockerfile`.
- Gateway generuje backend konfiguraci z uloženého výběru. Soubory se secrets zůstávají v privátním runtime úložišti, nikoli v image layers, CLI argumentech nebo logu. Změna výběru nesmí restartovat všechny zdravé kamery bez důvodu.
- Jeden image, všechny potřebné binárky. Žádný požadavek na další ručně spuštěný bridge či stažení `latest` při startu. Licenci a distribuci SDK/binárek řešit podle skutečných podmínek T01.
- Spravované procesy mají čitelné health stavy, omezené restart pokusy a korektní SIGTERM/shutdown včetně potomků. Interní administrace/ONVIF se nevystavuje do LAN. Ověřit platformy amd64 a aarch64 podle dostupných binárek.
- Hotovo: s prázdným `/data` se spustí onboarding, s připravenými testovacími daty skutečný restream. Pád backendu se promítne do stavu a obnoví bez ztráty autorizace nebo osamocených procesů.

### T05 — Xiaomi přihlášení a inventář

- Závislost: T01, T03, T04, smlouva T02. Rozdělit na login flow a následně inventář/výběr.
- Výstup: `internal/account/`, příslušné `/v2` handlery, testy fake poskytovatele. Použít doložený login podporovaný vybraným backendem; nevymýšlet Xiaomi OAuth či QR endpointy.
- Start/poll/cancel/expiry přihlášení, region, kontrola návratového state a uložení minimální obnovitelné autorizace. Raw heslo pokud možno vůbec nepřebírat. Autorizační data se neduplikují do HA entry.
- Inventář může být prázdný; zobrazí i nepodporovaná zařízení s důvodem. Nedělat nekonečné refresh/login smyčky. Obnovení účtu zachová stejný účet a nevyrobí nový inventář entit pod jinými ID.
- Hotovo: zrušený či expirovaný flow lze zopakovat, opakovaný callback nic neduplikuje, špatný region/prázdný účet má srozumitelný výsledek. Testy ověří redakci tokenů, cookies, přihlašovacích URL i tracebacků.

### T06 — Doplněk, propojení a bezpečné párování

- Závislost: T02–T05. Nejdříve samostatně prokázat síť a předání důvěry; potom balení.
- Výstup: `addons/xiaomi_local_gateway/`, `repository.yaml`, minimální pomocné stránky párování/přihlášení v `internal/web/`, jeden Container instalační příklad a dokumentované párování v `docs/API-MANAGED.md`. Uživatelské nastavení kamer zůstává v HA, nevytvářet druhý plný dashboard.
- Doplněk využije náš image, trvalé `/data`, ingress pro pomocné stránky a discovery v rozsahu ověřených HA API. Ověřit skutečný alias a dosažitelnost API i RTSP z HA Core. Host networking zvolit pouze podle ověřených požadavků discovery/transportu backendu.
- V HA OS předá credential důvěryhodný Supervisor discovery kanál; přesný payload i chování ověřit na HA OS. Pokud tento mechanismus nestačí, povinný mezikrok je úzce vymezený pairing proof, nikoli neautorizovaný endpoint vydávající tokeny.
- V HA Container se propojení potvrdí na lokální administrační stránce balíčku: krátce platný jednorázový kód/odkaz svázaný s konkrétním pokusem. Uživateli neukazovat dlouhodobý API token. Discovery je pouze nápověda, nikdy důkaz důvěry. Testovat replay, expirovaný kód, souběh dvou klientů a pokus cizího hosta.
- Pro pomocnou administraci Container varianty ponechat listener lokální; při přístupu z jiného zařízení použít ověřené HTTPS nebo autentizovaný tunel/proxy. T06 musí doložit celý tok z prohlížeče uživatele, nikoli jen serverové API. Klientské credentials lze odvolat a rotovat bez odhlášení Xiaomi účtu; HA je uloží privátně a diagnostika je rediguje.
- RTSP musí být dosažitelné z cílového HA kontejneru. Pouhý loopback uvnitř doplňku nestačí. Pro dosažitelný listener použít automaticky spravovanou autentizaci a správnou redakci URL; rozšířit dosavadní zákaz credentials pouze úzce a s testem, bez oslabení legacy validace. Žádné spoléhání na pouhé utajení názvu streamu.
- Hotovo: HA se propojí bez opisování tokenu, restart propojení zachová a neoprávněný klient ho nezíská. WSLc ověřuje Container variantu; Supervisor/discovery/ingress vyžadují oddělený test v HA OS. Bez něj doplněk není ověřený.

### T07 — Správce relací a obnova spojení

- Závislost: T02–T05. Fake manager testy lze psát před T04.
- Výstup: `internal/session/`, stavová mašina, fake clock testy a napojení status API. Přizpůsobit rozdělení odpovědností tomu, co již spravuje backend.
- Souběžné otevření dashboardu, snímku a Frigate sdílí jednu upstream relaci; případné další lokální kodekové varianty ji neduplikují. Explicitní odpojení/odebrání kamery zruší čekající reconnect.
- Ztrátu obrazu hodnotit podle přicházejících médií, ne pouze TCP/HTTP. Požadavek na video má počáteční timeout; bez aktivního diváka přejít do `idle`, ne falešně `offline`. Stálý odběr Frigate či uživatelská volba keep-alive je aktivní požadavek.
- Jeden reconnect mechanismus, exponential backoff s jitterem, výchozí čekání 1/2/4/8/16/30 s a strop 30 s. Refresh autorizace/klíče sloučit, aby souběžné kamery nevyvolaly login storm. Znovu vyhledat IP při DHCP změně; neukládat IP jako identitu.
- Chyba jedné kamery neblokuje ostatní. Auth expiry zastaví marné pokusy a vyvolá jeden požadavek na reauth. Stará relace se ukončí před náhradní. PTZ se neřadí do fronty k pozdějšímu vykonání po reconnectu.
- Hotovo: deterministické testy výpadku, souběhu, auth expiry, SIGTERM a reselect/deselect; žádné souběžné upstream relace ani růst gorutin po opakovaných cyklech. Fyzická obnova patří do T10.

### T08 — Jeden průvodce v Home Assistantu

- Závislost: T02, T05, T06; nejprve lze implementovat proti kontraktovým mockům.
- Výstup: úpravy `config_flow.py`, `api.py`, `const.py`, `strings.json`, `translations/cs.json`, `translations/en.json` a HA flow testy.
- Díly: (a) managed/advanced volba a spojení, (b) region a external/progress login krok, (c) výběr kamer se schopnostmi, (d) skutečné ověření snímku a shrnutí. Čekání je neblokující, má konec a možnost zrušení.
- Spravovaný režim je výchozí. Chybějící doplněk v HA OS nabídne ověřený instalační odkaz/postup a pokračování. V HA Container nabídne konkrétní postup pro jeden runtime; nepokouší se spouštět Docker z HA procesu.
- Přihlašovací stránku přenášet bezpečně v kontextu T06. Nevkládat dlouhodobý token do discovery, URL prohlížeče ani redirect. Po návratu pokračovat v témže flow; restart flow nemaže již uložený účet.
- Po timeoutu obrazu nabídnout „Zkusit znovu“, diagnostiku a výslovné uložení jako offline; nikdy neukázat úspěšné ověření. U nepodporovaného modelu zobrazit důvod a bezpečnou diagnostiku.
- Hotovo: uživatel přidá model podporovaný fake backendem bez URL/tokenu/YAML, dvojí spuštění nevytvoří duplicity, zavření a nové otevření průvodce funguje. Testy všech kroků, chyb, timeoutu, zrušení i návratu z loginu; ručně ověřit české UI a fyzický obraz v T10.

### T09 — Dostupnost, reauth a diagnostika v HA

- Závislost: T07–T08. Rozdělit na availability/lifecycle, následně reauth/options a diagnostiku.
- Výstup: `__init__.py`, `coordinator.py`, `entity.py`, `camera.py`, `button.py`, nové `sensor.py` / `diagnostics.py` a související testy.
- Oddělit nedostupný server (`ConfigEntryNotReady` / retry), odmítnutý gateway token (nové párování/reauth) a Xiaomi `auth_required` (obnova účtu, bez zahazování zdravých streamů). Jedna chyba nesmí zakládat opakované Repair položky.
- Mapovat HA availability na jednotlivou kameru a capability, ne pouze na existenci ID. `idle` popsat odděleně od streaming; při požadavku ověřit snímek/obraz. PTZ entity při ztrátě schopnosti znepřístupnit, i pokud kamera dál existuje.
- Doplnit snapshot přes stejnou relaci a jasné hlášení nepodporovaného kodeku. Nejprve preferovat kamerou podporovaný H.264 profil; případné H.265→H.264 převádění je výslovná volba s omezením prostředků, ne skrytá zátěž slabého HA zařízení.
- Options mění výběr kamer a kvalitu. Dočasná nedostupnost nemaže registry ani záznamy o vybraných kamerách. Unload/reload odstraní listenery a background tasks. Migrace v1 zachová dosavadní unique IDs, entity IDs a automatizace; URL přestane být novou identitou.
- Diagnostika: verze, model/firmware, capability, stav, poslední frame, příčina a počty obnov. Redigovat authorization, cookies, URL credentials/query, IP/MAC, device/account IDs a párovací údaje podle explicitní allowlist politiky. Ani debug log nesmí vracet secrets.
- Hotovo: HA testy ověří jeden reauth bez nové entry, výpadek jedné ze dvou kamer, migraci v1 a unload/reload bez duplicit či úniku úloh. Veřejné testy nahradí závislost na skrytých `.test-env` skriptech.

### T10 — Skutečný C201 pilot a test stability

- Závislost: T04–T09. Výstup: `docs/validation/<datum>-c201.md` s verzemi, příkazy, exit kódy, časováním a známými limity. Soukromé logy/video zůstávají mimo Git.
- Test čisté instalace v Container i HA OS: účet → výběr → skutečný snímek → HA živý obraz. Ověřit v používaném prohlížeči; RTSP odpověď či HA stav `idle` nestačí. Nepřevzít starý mock report jako důkaz videa.
- Restart HA, runtime a kamery; změna IP, krátký výpadek sítě; kontrolovaný restart testovacího routeru nebo simulace linku. Neodpojovat produkční domácí síť bez výslovné autorizace pro tento zásah.
- Rozlišit ztrátu cloud přístupu během běžící relace, pokus o novou relaci bez cloudu a návrat cloudu. Při odmítnutém tokenu se objeví jediný reauth, nevznikne opakovaný login storm. Prokázat LAN cestu médií dostupnými statistikami nebo sanitizovaným capture; nezamlčet nemožnost vyloučit relay.
- Současný HA stream + snapshot + Frigate restream nezvýší upstream relace nad jednu na zařízení. Frigate recording/ONVIF tracking zůstávají samostatné capability; nepodmiňovat jimi první video release.
- Bezpečně ověřit fyzické PTZ jen pro doložené modelové mapování. GUI nesmí nabízet neověřené presets nebo tracking.
- **Cíle pro release na referenční zdravé LAN** (návrh, ne naměřený výsledek): první snímek do 15 s od požadavku po autorizaci, oznámení přerušeného aktivního streamu do 30 s, obnova do 120 s po návratu sítě při platné autorizaci, všech 10 řízených cyklů obnovy bez ručního restartu. Zapsat měření a případnou odůvodněnou změnu cíle před uvolněním.
- Běh 72 hodin s průběžným záznamem reconnectů, snímků, CPU/RAM a počtu relací/procesů. Žádné ruční zásahy; vyšetřit monotónní růst paměti/úloh po zahřátí a po návratu z výpadků. Zapsat limity hostitele a sampling interval. Dlouhý test neblokuje další práci a nesmí být prohlášen za hotový před uplynutím doby.
- Hotovo: oddělené výsledky pro simulaci, Container, HA OS a C201; žádné zamlčené neprovedené části. Kompatibilita uvádí model ID + firmware + region + backend revizi, video a PTZ odděleně.

### T11 — Bezpečná aktualizace, rollback a návod

- Závislost: T10 pro stabilní release; build/release scaffolding může vzniknout dřív.
- Výstup: CI pro Go + HA behaviorální testy + kontrakt + image smoke test, připnuté multiarch image, release manifest se spárovanými verzemi backend/gateway/integrace, release notes a krátký instalační návod.
- Před aktualizací záloha `/data` a známá verze schématu. Nový release migruje atomicky; downgrade nesmí číst nekompatibilní data. Rollback obnovuje předchozí image i odpovídající data, nikoli jen image tag. Otestovat neúspěšnou migraci a návrat starého funkčního stavu.
- U doplňku ověřit start on boot, watchdog a HA backup/restore. U Container uvést stejný datový svazek, restart policy a konkrétní postup obnovy. Nevytvářet nové účty nebo identity při běžném updatu.
- Návod oddělí HA OS instalaci od Container instalace a po instalaci používá tentýž průvodce. Pokročilé RTSP/ONVIF a Frigate mají vlastní volitelnou stránku. Uvést původní párování v Xiaomi Home, LAN i cloud omezení a dostupné regiony/platformy podle skutečných testů.
- Hotovo: čistá instalace, upgrade ze stávající v1 konfigurace a rollback projdou bez duplikovaných entit a bez opakovaného zadávání účtu při zachované autorizaci. Publikování repozitáře/images je samostatný krok po dokončení lokálního výsledku.

## 6. Pořadí a pravidla práce levného modelu

```text
T00 -> T01 (C201/backend brána) -> T02 -> T03 -> T04 -> T05
T05 -> T06 -> T08
T05 -> T07
T07 + T08 -> T09 -> T10 -> T11
```

Pracovat sekvenčně; diagram popisuje závislosti, není zadáním pro subagenty. Pokud chybí hardware nebo účet pro T01, dokončit dokumentaci a na backendu nezávislou část T02/T03 i mock testy. Produkční výběr backendu a podporu C201 neoznačit za hotové.

1. Před změnou přečíst `git status --short` a diff relevantních souborů. Nezasahovat do `.codex` konfigurace, tokenů ani cizích rozpracovaných změn.
2. Pro jednu iteraci vybrat jediný úkol nebo jeho uvedený díl. Nejprve otevřít vyjmenované soubory; nerozšiřovat zbytečně kontext o celý repozitář či historii.
3. Držet kontrakt z T02. Novou nutnou odchylku zapsat s důvodem a upravit klienta i test smlouvy společně; nepřidávat endpointy podle domněnek o upstreamu.
4. Měnit co nejmenší funkční celek. Testy mají prokázat chování a chyby důležité pro konkrétní krok. Nevytvářet obecné frameworky, vlastní transport nebo celkové přeformátování projektu.
5. U Go změn `go test -race ./...`, `go vet ./...` v připraveném builderu; u HA změn skutečné pytest flow/entity testy a syntax. `check_config` pro změny runtime config. Build a živý smoke test pro změny balení. Syntax sama nestačí.
6. Stav uložit do `docs/IMPLEMENTATION-STATUS.md`: úkol/díl, dotčené soubory, provedené testy s exit kódem, důkaz, otevřené omezení a přesný další krok. Logy/fotky/klíče neukládat do veřejného reportu.
7. Žádné tvrzení „stabilní“ před T10. Nepoužívat mock kameru jako úspěšný C201 test. Bez testovacího zařízení pokračovat nezávislou částí a vypsat potřebný vstup; nevyrábět hardware výsledky.

Připravený úvodní prompt je v [NEXT-TASK.md](NEXT-TASK.md).

## 7. Podklady a meze zjištění

Kontrolováno 5. 11. 2026; webové zdroje mohou být cachované. Před implementací T01 ověřit konkrétní revizi a její skutečné chování:

- [HA App configuration](https://developers.home-assistant.io/docs/apps/configuration/) a [App communication](https://developers.home-assistant.io/docs/apps/communication/): konfigurace doplňku, trvalé `/data`, síťové aliasy, komunikace se Supervisorem. Nejsou důkazem, že HACS automaticky instaluje doplňky.
- [HA config flow](https://developers.home-assistant.io/docs/core/integration/config_flow/): průvodce, discovery, stabilní unique ID, migrace a reauth existující entry. URL/IP není vhodné nové unique ID.
- [xiaomi-onvif](https://github.com/SincereXIA/xiaomi-onvif): integrovaná distribuce patched go2rtc + ONVIF, verifikace vyjmenovaných modelů a beta omezení. Uvedená podpora jiných zařízení není důkazem C201.
- [C201 issue v go2rtc](https://github.com/AlexxIT/go2rtc/issues/2290): report o chybějící podpoře; sám o sobě neprokazuje aktuální nekompatibilitu všech backendů a firmwarů.
- [ha-xiaomi-camera](https://github.com/willjayyyy/ha-xiaomi-camera): referenční balení integrace a doplňku s vendor streaming knihovnou. README uvádí testování jen čínského regionu, chybějící PTZ a samostatné licenční podmínky binárních součástí. Převzetí této cesty vyžaduje ověření našich požadavků.

Další funkce až po stabilním video pilotu: vlastní transport, více účtů, více objektivů, talkback, SD playback, automatické Frigate konfigurace, autotracking a pokročilý PTZ dashboard. Zachovat možnost pozdějšího rozšíření pomocí capabilities, ale nepřenášet tyto práce do prvního přidání C201.
