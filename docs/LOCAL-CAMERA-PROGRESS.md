# Postup práce podle LOCAL-CAMERA-HANDOFF

| Úkol | Stav | Výsledek |
| --- | --- | --- |
| A00 — izolovaná Go + HA 2026.10.4 baseline | pass, 2026-11-12 | Go `-race`, `go vet`, gateway Python testy, HA `check_config`, HA entity/settings unit testy, čerstvý setup, mock snapshot/HLS/RTSP, outage/recovery a `late-settings` E2E prošly. HA image digest ověřen. E2E výstup potvrzuje config entry `loaded`, 1× Night Shot `Auto`, pět stavů `on/on/off/on/off`, potom `unavailable` a nulové volání `/settings` před zveřejněním schopnosti. |
| A01 — versionovaný capability evidence | pass, 2026-11-12 | Volitelný schema v1 v modelu/API, exact-C201 evidence, cloud dependencies, HA state attribute, tolerování staré/neznámé verze a provider capability filtering. Kompletní izolovaná sada Go race, vet, 13 Python testů a HA 2026.10.4 E2E prošla; podporu zobrazil `late-settings` scénář. |
| A02 — diagnostický runner | in progress | Přidán omezený read-only `camera-probe` preflight/dry-run s explicitním opt-in inventáře a sanitizovaným reportem; transportní probe, existing-session telemetry a další recipes čekají. |
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

## A01 — implementační detail a limit

Sdílený Go model validuje verzi schématu, enumy, názvy, limity a bezpečný formát odkazů/firmware. API dělá deep copy, podporu PTZ/settings odebere, pokud odpovídající provider není nainstalován. HA při chybějícím nebo neznámém schema v1 zachová kameru a staré capability; známá bezpečná metadata vystaví jako atribut `support` kamery. `offline` značí pouze automatizovaný mock test. Video/PTZ mají deklarované `path=camera_lan` spolu s explicitními cloud kroky pro účet, inventář a/nebo vytvoření relace; nové připojení proto není tvrzeno jako offline.

## A02 — bezpečný preflight, částečný postup

- Přidáno `internal/diagnostics` a `cmd/camera-probe`.
- Bez `-inventory`: jen pevné GET `/healthz` a `/v2/info`; `-dry-run` vypíše plán bez jediného síťového požadavku. Token je čten pouze z předem nastaveného `XLG_API_TOKEN`.
- S explicitním `-inventory`: přidá GET `/v2/cameras`, který může obnovit Xiaomi inventory přes cloud. Žádná další API/metoda není dostupná; příkaz neotevírá stream ani session, neposílá PTZ ani zápis.
- Report má deadline 8 s, limit odpovědi 1 MiB, maximálně 128 kamer; blokuje veřejný gateway cíl a redirecty. Neobsahuje token, jméno kamery, raw ID, IP adresu nebo RTSP URL; camera ID pseudonymizuje. Endpoint class je deklarovaný záznam API, ne síťové měření.
- Offline testy: dry-run bez requestů; pouze GET a autorizace; kontrola redakce identity/adres/URL/tokenu; odmítnutí veřejného cíle; camera ID vyžaduje explicitní inventory.
- Ověření: `scripts/test-local.ps1`, log `.test-env/evidence/test-local-20261112-170429-d8ef6955.log`, `RESULT=PASS`. Go race testy zahrnuly `internal/diagnostics`; Go vet a HA 2026.10.4 E2E prošly. Gateway hash `31002f5e9c3390ab9cb9eb0520f06ae6b7e3c0fb951b9389e754fdac0bcf70fc`, HA integrace `895b72a41de66c5ca1a8076d0ee4079407676bfed0119b504c06f7c0d06b38b6`, E2E/mocks `fa7b864cdaf4523bf1215c0a76cebd708e34a81bb54460fe61fb09d0903c0dab`.
- Zbývá do A02: sdílení stávající session bez druhého dialu, read-only recipes pro transport/protokol, korelační ID, cancel testy, bounded event buffer a ověření gateway timeoutů. A03 síťový peer/WAN test zatím nezačal. Fyzická C201 nebyla kontaktována (`C201_VIDEO=UNVERIFIED`).

```powershell
go run ./cmd/camera-probe -gateway http://127.0.0.1:8099 -dry-run
# Skutečný inventory GET vyžaduje předem nastavené XLG_API_TOKEN a explicitní opt-in:
go run ./cmd/camera-probe -gateway http://127.0.0.1:8099 -inventory
```

