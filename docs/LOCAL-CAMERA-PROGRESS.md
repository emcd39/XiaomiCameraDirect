# Postup práce podle LOCAL-CAMERA-HANDOFF

| Úkol | Stav | Výsledek |
| --- | --- | --- |
| A00 — izolovaná Go + HA 2026.10.4 baseline | pass, 2026-11-12 | Go `-race`, `go vet`, gateway Python testy, HA `check_config`, HA entity/settings unit testy, čerstvý setup, mock snapshot/HLS/RTSP, outage/recovery a `late-settings` E2E prošly. HA image digest ověřen. E2E výstup potvrzuje config entry `loaded`, 1× Night Shot `Auto`, pět stavů `on/on/off/on/off`, potom `unavailable` a nulové volání `/settings` před zveřejněním schopnosti. |
| A01 — versionovaný capability evidence | pass, 2026-11-12 | Volitelný schema v1 v modelu/API, exact-C201 evidence, cloud dependencies, per-feature availability/reason, HA state attribute, kompatibilita starých/neznámých dat a izolované zahazování vadných optional metadat. Smíšený inventář vrací všechny kamery bez poškození capabilities. |
| A02 — diagnostický runner | pass (offline), 2026-11-12 | Pojmenované operace `session_status` a `transport_metadata`, generace relace, korelační ID, 32položkový sanitizovaný event ring, deadline/cancel a serializace čtení existující fake session jsou ověřené přes běžící izolovanou gateway. Dry-run má nula síťových operací. Živé měření kamery patří do A03/A04 a zůstává neověřené. |
| A03–A04 | queued | Síťová měření/bezpečnost peeru → aktuální HA/C201 přejímka. Aktuální běh neoslovil fyzickou kameru. |

## Evidence A00

- Zdrojový commit: `3cb89e8ef5eda592a3cb3a0579d29740b6c03f8b`.
- Příkaz: `scripts/test-local.ps1`.
- Výsledek: `RESULT=PASS`, exit 0; testovací kontejnery a jejich unikátní síť byly odstraněny.
- HA: `2026.10.4`, digest `sha256:23f4dfbb69ce76315dbc4de7915504f1c2dcecd7ca2648113a493a385972e133`; runtime container image ID `sha256:8e7d93e48bed8980ec6924f8620928efa3bf981e3d33aa3be047944f37c6cd3a`.
- Gateway race suites + vet prošly; 11 klientských testů; kamera stream capability 1; settings retry/gate/entities 3; `late-settings` E2E exit 0.
- Sanitizovaný log: `.test-env/evidence/test-local-20261112-162635-f49fca83.log`.
- Závěr je pouze izolovaný mock HA/gateway důkaz. Fyzický C201 nebyl kontaktován a živý přenos zůstává mimo výsledek této sady.

## Evidence A01

- Příkaz: `scripts/test-local.ps1` nad aktuálními změnami A01.
- Výsledek: `RESULT=PASS`, exit 0; testovací kontejnery a jejich unikátní síť byly odstraněny.
- Log: `.test-env/evidence/test-local-20261112-164617-9d04a402.log`.
- Ověření: Go `-race ./...`; `go vet ./...`; 13 Python gateway klientských testů; HA check_config; camera stream + 3 settings unit testy; setup, stream/snapshot, outage/recovery; `late-settings` E2E s `support_diagnostics=versioned_and_cloud_dependency_visible`.
- E2E settings výsledek: config entry `loaded`; před capability žádný `/settings`; Night Shot `Auto`, binary `on/on/off/on/off`, následně `unavailable`; `/settings` 2× po zveřejnění capability. Camera support metadata obsahovala `camera_settings_read`, `path=cloud`, `requires_cloud=[account_auth, inventory, settings_read]`.
- Zdrojové hashe v logu: gateway `89cac70d8f6eb7d34889452bfd5b2071be6d9df64b76f3b776cbb11d6e0e530a`; HA integrace `895b72a41de66c5ca1a8076d0ee4079407676bfed0119b504c06f7c0d06b38b6`; E2E/mocks `c8e2bd7aafdeafe7885f0e1a9efdf46d2a23b08054d6f419913a1f513b77ebc1`.
- Omezení: `path` je deklarovaná implementační cesta, nikoli pozorování paketů. Historické `device` důkazy pro video/PTZ nepředstavují nové živé měření; aktuální síťová trasa ani C201 nebyly v běhu kontaktovány. `C201_VIDEO=UNVERIFIED`.

## A01 availability a A02 metadata existující session

- Příkaz: `scripts/test-local.ps1`; gateway Go race testy, `go vet ./...`, HA 2026.10.4 headless sada.
- Výsledek: `RESULT=PASS`, exit 0; všechny kontejnery a unikátní síť tohoto běhu byly po testu odstraněny.
- Log: `.test-env/evidence/test-local-20261112-173620-d360cd10.log`.
- Source SHA-256: gateway `ecc505326b272751c3e625b04b63b700c44111253a6b5ea9598f3a23a573394a`; HA integrace `b70f2c4726c5966fefa738a5727ff7009f78afa0cbb4027749c24d11e059ddfa`; E2E/mocks `bb4242580ac6a4adf1e2ce35594353fbca4314b42ed4b2eb10b96ed1b3d88caa`.
- Go race zahrnul regression proxy bypass pro privátní IPv4 target s nastavenými `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY` a prázdným `NO_PROXY`; transport nepoužil proxy a dotaz zasáhl pouze fake gateway. Zahrnul také opt-in/auth test nové route, 404 při vypnutí, feature advertisement, timeout/cancel a čtení existující fake session bez nového připojení, rámce, close ani PTZ zápisu.
- A01 regression ověřila, že kamera se známým support, neznámou verzí schématu a vadným volitelným záznamem zůstávají všechny v HTTP 200 inventáři. Vadná metadata jsou vynechána, původní core capabilities zůstávají.
- Python: 15 klientských testů. HA `check_config`, kamerové capability testy, 3 C201 settings testy a kompletní setup/outage/recovery prošly. `late-settings`: entry `loaded` před capability, kamera/PTZ dostupné, první přechodná chyba `/v2/info`, Night Shot `Auto`, binární stavy `on/on/off/on/off` (indicator/motion/human/move/pet), následně Night Shot `unavailable`; před capability nebyl volán `/settings`.
- Omezení měření: tento běh používal pouze mock RTSP/gateway a nikoho nepřipojoval k fyzické C201. `C201_VIDEO=UNVERIFIED`; aktuální peer class ani WAN-offline chování nejsou tímto výsledkem doložené. Diagnostický route je výchozím stavem vypnutý a vyžaduje `-enable-diagnostics`.

## A01 — implementační detail a limit

Sdílený Go model validuje verzi schématu, enumy, názvy, limity a bezpečný formát odkazů/firmware. API dělá deep copy, podporu PTZ/settings odebere, pokud odpovídající provider není nainstalován. HA při chybějícím nebo neznámém schema v1 zachová kameru a staré capability; známá bezpečná metadata vystaví jako atribut `support` kamery. `offline` značí pouze automatizovaný mock test. Video/PTZ mají deklarované `path=camera_lan` spolu s explicitními cloud kroky pro účet, inventář a/nebo vytvoření relace; nové připojení proto není tvrzeno jako offline.

## A02 — bezpečný preflight a čtení existující session

- Přidáno `internal/diagnostics` a `cmd/camera-probe`.
- Bez `-inventory`: jen pevné GET `/healthz` a `/v2/info`; `-dry-run` vypíše plán bez jediného síťového požadavku. Token je čten pouze z předem nastaveného `XLG_API_TOKEN`.
- `-inventory` je explicitní opt-in na GET `/v2/cameras`; Xiaomi provider může tímto požadavkem obnovit inventory přes cloud. `-session` vyžaduje `-camera-id` i explicitní inventory. `-session-operation` povoluje pouze `session_status` nebo `transport_metadata`; druhá operace vyžaduje přesnou `-session-generation` vrácenou první operací. Autorizovaná gateway route `/v2/cameras/{id}/diagnostics/{operation}` vyžaduje `-enable-diagnostics` a ve výchozím stavu je vypnutá.
- Runner má celkový deadline 8 s, limit odpovědi 1 MiB a maximálně 128 kamer; odmítá veřejný gateway cíl, přesměrování a env proxy. Sanitizovaný report neobsahuje token, jméno kamery, raw ID, raw peer IP, RTSP URL ani média; camera ID pseudonymizuje.
- Session status vrací pouze povolený stav a opaque generaci. `transport_metadata` čte protokol, klasifikaci peeru a čítače z již otevřené relace stejné generace. Čtení nepřipojuje ani nerestartuje relaci a nečte frame. Každé volání dostává serverové korelační ID a nejvýše 32 allowlistovaných událostí; timeout/zrušení a čekání na zámek se testují bez kamery. Třída peeru je pozorování jedné konkrétní relace, ne důkaz všech síťových operací.
- Offline regrese: dry-run bez requestů; privátní cíl s nastraženou proxy (proxy 0 requestů); smíšený inventář s vadným support; pouze GET/auth; redakce; odmítnutí veřejného cíle; disabled route; cizí camera ID; zastaralá generace; falešná běžící session bez nového připojení; timeout/cancel, uvolnění zámku a limit event ring.
- Ověření A02: `scripts/test-local.ps1`, log `.test-env/evidence/test-local-20261112-181029-97b54b3f.log`, `RESULT=PASS`; Go `-race ./...`, `go vet ./...`, HA 2026.10.4 včetně `late-settings` prošly. Samostatně `C:\Apps\go\bin\go.exe test ./... -count=1` v `xiaomi-diagnostic-console` a `node scripts/Test-Preview-UI.js` prošly; log `.test-env/evidence/test-console-20261112-181833.log`.
- Headless UI ověřil současně maximálně jeden status request i při 5s latenci; UI se aktualizovalo a PTZ šipky se znovu povolily. Testovací gateway ověřila cizí ID, zastaralou generaci, cancel/timeout s následným uvolněním zámku a ring omezený na 32 událostí.
- Ověření je výhradně offline/mock. Žádná živá kamera ani fyzická C201 nebyla kontaktována; `C201_VIDEO=UNVERIFIED`. Peer class, chování při WAN blokaci a end-to-end lokálnost zůstávají A03/A04.

## A02 — zrušitelné čekání na zámek inventáře, 2026-11-12

- `CameraDiagnostics` při opětovném ověření identity zdroje používá `TryRLock` a čekání přerušitelné kontextem. Během držení `source.lifecycleMu` už neblokuje na `p.mu.RLock()` bez deadline; po získání inventářového read locku stále kontroluje, že `p.sources[id]` ukazuje na stejný zdroj, a pak kontroluje generaci běhu.
- Přímý test `TestCameraDiagnosticsInventoryLockWaitHonorsContextAndReleasesLifecycle` pokrývá cancel i deadline při záměrně drženém inventářovém write locku. Dokud jej jiná operace drží, diagnostika skončí a `lifecycleMu` lze ihned získat. Po odemčení uspěje aktuální generace a stará generace skončí před voláním metadata readeru.
- Cílený test bez race detektoru prošel příkazem `C:\Apps\go\bin\go.exe test ./internal/xiaomi -run '^TestCameraDiagnosticsInventoryLockWaitHonorsContextAndReleasesLifecycle$' -count=1`.
- Projektový izolovaný runner dokončil Go race testy (`go test -race ./...`), `go vet ./...` i kompletní HA 2026.10.4 headless sadu včetně setup, stream/snapshot, outage/recovery a `late-settings`: `RESULT=PASS`. Log: `.test-env/evidence/test-local-20261112-182832-8bb56eca.log`. Nová regrese je zahrnutá v race sadě balíčku `internal/xiaomi`.
- Testy používaly pouze fake session a mock HA/gateway. Fyzická C201 nebyla kontaktována; `C201_VIDEO=UNVERIFIED`.

```powershell
go run ./cmd/camera-probe -gateway http://127.0.0.1:8099 -dry-run
# Skutečný inventory GET vyžaduje předem nastavené XLG_API_TOKEN a explicitní opt-in:
go run ./cmd/camera-probe -gateway http://127.0.0.1:8099 -inventory
```

