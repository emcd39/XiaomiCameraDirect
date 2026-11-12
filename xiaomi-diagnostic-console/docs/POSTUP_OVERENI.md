# Postup ověření na skutečné Xiaomi C201 (bez automatického nasazení)

Tento dokument je operační checklist pro Windows. Sám o sobě nedává svolení měnit gateway ani pohnout motorem. Všechny fyzické změny probíhají **až po samostatném schválení**.

## Fáze 0 – fyzické podmínky

- Kamera je skutečně Xiaomi Smart Camera C201; firmware a IP zkontrolovat ve Xiaomi Home / síti. Poslední oznámená IP je `192.168.2.124`, **aktuálně nebyla nezávisle ověřena**.
- V Xiaomi Home vyzkoušet, že PTZ funguje. Vypnout automatické sledování objektů, vybrat statickou scénu s dostatkem textur. Zkontrolovat fyzické limity pohybu.
- Mít možnost vrátit kameru do původního stavu. Nepoužívat před živým testem hromadné příkazy.

## Fáze 1 – dokončit E1.3

- Ověřit finální runbook `diagnostika/C201_E1_PREDANI_FINAL.md` v reálném repozitáři.
- Dosavadní dokument implementuje ochranu opakovaného `-data-dir`, **ale v nahrané verzi nebyla doložena regrese 20f**. Nejprve dokončit její offline výstup.
- Neměnit `gateway.exe` vytvořenou 2026-11-09 bez nové sady testů; historický kontrolní SHA-256: `591DB7243482A7498B8ACEABE76583F63C924B470BE3A46B8F76601AAAFF7FB0`.

## Fáze 2 – lokální diagnostická konzole MOCK

1. Rozbalit balíček a spustit `scripts\Start-Demo.ps1`.
2. Otevřít `http://127.0.0.1:18880`.
3. Spustit syntetický obraz, ověřit snímky/FPS, zastavit a spustit znovu.
4. Načíst mock inventář, ověřit 1 × C201.
5. Zkusit simulovaný levý krok po zadání potvrzení; výsledek musí být `simulated_only`, `not_measured`.
6. Nahrávání v mock režimu musí být zakázané. Nesmí běžet žádný FFmpeg nebo CS2 proces pro kameru.
7. Spustit `scripts\Smoke-Test.ps1` (pouze čtení).

## Fáze 3 – čerstvá kontrola skutečné gateway (read-only)

- Nespoléhat na historické PID `38416`. Zjistit aktuální PID, executable, čas startu, konkrétní `data-dir`, porty, skutečný listen stav, login/DPAPI profil a firewall.
- Ověřit, že dosavadní gateway/video pracuje právě s jedinou C201 session. Nevykazovat starý log jako aktuální session.
- Pokud selže CIM, socket preflight, ověření identity, efektivní firewall nebo přístup k existujícímu `managed-state.json`, přerušit test **před odstávkou**.
- Odsouhlasit samostatné provedení E1 výměny/rollbacku. Mini klient nikdy nezastavuje gateway.

## Fáze 4 – video bez PTZ

1. Připravit `config.live.json` z příkladu. `ptz.enabled` musí zůstat `false`.
2. Spustit konzoli pouze na loopbacku.
3. Číst `/api/status`; to vyvolává bezpečné `/healthz` a `/v2/info`, nikoli `/v2/cameras`.
4. Je-li potřeba, ručně vyvolat `Načíst inventář*` a počítat s možností vytvoření CS2 session. Bez automatického retry.
5. RTSP URL se bere výhradně z připravené vybrané C201 v managed inventáři. Pokud ji inventář neposkytuje, náhled ani nahrávání nespouštět; nepoužívat statický soubor `rtsp_url_file`, který nelze bezpečně spárovat s vybranou kamerou.
6. Ručně spustit video. Zkontrolovat reálné snímky, rozlišení, dekódování a stabilitu. V případě chyby zkontrolovat lokálně FFmpeg a gateway log. Konzole neuchovává raw snímky (mimo případného nahrávání).
7. Znovu ověřit, že počet CS2 session v gateway zůstal 1 i po připojení RTSP klienta.

## Fáze 5 – lokální úložiště

1. Zvolit `record_dir` s dostatečnou kapacitou a oprávněním konkrétního Windows uživatele; pro NAS připojit důvěryhodný SMB share (např. UNC, ověřit přístup a rychlost).
2. Spustit nahrávání, nechat běžet aspoň jeden segment, zastavit.
3. Ověřit MKV soubor: použít `ffprobe -v error -show_streams <cesta>` a běžný přehrávač. Zkontrolovat HEVC, časovou délku a plynulost.
4. Ověřit seznam a stažení přes web UI.
5. Nahrávání do NAS **není důkaz čtení SD karty**. Jeho kapacita/seznam záznamů/mazání nejsou zatím v projektu implementovány.

## Fáze 6 – jeden krok vlevo (samostatně schváleno)

1. Čerstvě potvrdit fázi 0.
2. Potvrdit aktuální PID/start UTC a `probe.exe` + runtime verifier + stderr log dané běžící instance.
3. Změnit **pouze** `ptz.enabled` na `true`, doplnit přesné cesty a identitu, restartovat **jen konzoli** pro načtení její konfigurace.
4. Zkontrolovat obraz před testem; nejméně osm souvislých stabilních snímků měří již existující helper, max drift 2 px.
5. Zaškrtnout fyzické potvrzení, zadat `ONE-LEFT-APPROVED`, jednou kliknout vlevo. Console spustí **jediný proces pomocného PTZ helperu bez retry**.
6. Vyhodnotit **zvlášť**: `HTTP write_status`, `0x113`, obrazový posun, viditelný fyzický pohyb. Ve webovém přehledu může stát `unverified`; podrobnosti jsou v sanitizované evidenci helperu/gateway. Ani HTTP 200, ani `reply_received` není fyzický důkaz.
7. Při timeoutu **neopakovat** automaticky. Nejprve vyhodnotit, zda kamera už neprovedla pohyb. Při problému nezkoušet další směry.

## Fáze 7 – SD a další funkce

Aktuálně klient **neobsahuje** příkazy pro SD kartu, IR, detekci, audio, presety ani směr PTZ kromě LEFT. Doplnění musí začít auditem skutečných read-only možností gateway / MIoT a lokálního transportu. Žádné neznámé MIoT zápisy naslepo.

### Kritéria úspěchu

| Test | Doklad úspěchu |
|---|---|
| Webové UI | Lokální stránka funguje, bez připojení ke kameře v mocku |
| Skutečný obraz | Dekódované aktuální snímky z C201, přes existující gateway RTSP |
| Stabilita video | Souvislá baseline se známým rozlišením, bez další CS2 relace |
| PTZ zápis | Výstup helperu a gateway `0x112` |
| PTZ odpověď | Samostatný záznam `0x113` nebo explicitní `no_reply` |
| Fyzický pohyb | Obrazový posun pevných bodů a vizuální potvrzení |
| Nahrávání | MKV segment skutečně vytvořen a přehratelný |
| Přístup k SD | Zatím `UNVERIFIED`, nelze vykazovat jako PASS |
