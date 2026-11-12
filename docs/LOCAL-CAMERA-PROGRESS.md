# Postup práce podle LOCAL-CAMERA-HANDOFF

| Úkol | Stav | Výsledek |
| --- | --- | --- |
| A00 — izolovaná Go + HA 2026.10.4 baseline | pass, 2026-11-12 | Go `-race`, `go vet`, gateway Python testy, HA `check_config`, HA entity/settings unit testy, čerstvý setup, mock snapshot/HLS/RTSP, outage/recovery a `late-settings` E2E prošly. HA image digest ověřen. E2E výstup potvrzuje config entry `loaded`, 1× Night Shot `Auto`, pět stavů `on/on/off/on/off`, potom `unavailable` a nulové volání `/settings` před zveřejněním schopnosti. |
| A01 — versionovaný capability evidence | pass, 2026-11-12 | Volitelný schema v1 v modelu/API, exact-C201 evidence, cloud dependencies, HA state attribute, tolerování staré/neznámé verze a provider capability filtering. Kompletní izolovaná sada Go race, vet, 13 Python testů a HA 2026.10.4 E2E prošla; podporu zobrazil `late-settings` scénář. |
| A02–A04 | queued | Diagnostický runner → síťová měření/bezpečnost peeru → aktuální HA/C201 přejímka. Aktuální běh neoslovil fyzickou kameru. |

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
- E2E settings výsledek: config entry `loaded`; před capability žádný `/settings`; Night Shot `Auto`, binary `on/on/off/on/off`, následně `unavailable`; `/settings` 2× po zveřejnění capability. Camera support metadata obsahovala `camera_settings_read`, `path=cloud`, `requires_cloud=[settings_read]`.
- Zdrojové hashe v logu: gateway `89cac70d8f6eb7d34889452bfd5b2071be6d9df64b76f3b776cbb11d6e0e530a`; HA integrace `895b72a41de66c5ca1a8076d0ee4079407676bfed0119b504c06f7c0d06b38b6`; E2E/mocks `c8e2bd7aafdeafe7885f0e1a9efdf46d2a23b08054d6f419913a1f513b77ebc1`.
- Omezení: `path` je deklarovaná implementační cesta, nikoli pozorování paketů. Historické `device` důkazy pro video/PTZ nepředstavují nové živé měření; aktuální síťová trasa ani C201 nebyly v běhu kontaktovány. `C201_VIDEO=UNVERIFIED`.

## A01 — implementační detail a limit

Sdílený Go model validuje verzi schématu, enumy, názvy, limity a bezpečný formát odkazů/firmware. API dělá deep copy, podporu PTZ/settings odebere, pokud odpovídající provider není nainstalován. HA při chybějícím nebo neznámém schema v1 zachová kameru a staré capability; známá bezpečná metadata vystaví jako atribut `support` kamery. `offline` značí pouze automatizovaný mock test. Video/PTZ mají deklarované `path=camera_lan` spolu s explicitními cloud kroky pro účet, inventář a/nebo vytvoření relace; nové připojení proto není tvrzeno jako offline.

