# Zadání pro Codex – integrace a ověření mini klienta

## Fáze K1 – nasazení samotné konzole, bez kamery

Pracuj v `C:\DEV\xiaomi-local-gateway`. Rozbal `xiaomi-diagnostic-console` do `tools\diagconsole` (případně vedle repozitáře). Proveď jen `go test ./...` a `scripts\Start-Demo.ps1`, otevři lokální UI a ověř mock video + PTZ simulaci. Neprováděj deployment gateway. Zachovej existující necommitnuté soubory.

**Akceptace:** Start demo bez nového CS2 spojení, syntetické JPEG snímky se zobrazí, mock PTZ je viditelně označen jako simulace, inventory se nenačítá automaticky.

## Fáze K2 – live read-only, bez motoru

Pouze po samostatném schválení a ověření skutečného procesu: nastavit `config.live.json`, ponechat `ptz.enabled=false`, pouze číst `/healthz` a `/v2/info`, případně ručně číst `/v2/cameras` s vědomím, že může navázat session, a ručně spustit RTSP preview. Žádná automatická obnova session a žádný restart.

**Akceptace:** Reálné dekódované snímky ze správné kamery, stabilní FPS, žádné vytvoření další CS2 session. V případě neověřené identity výsledek STOP, nikoli PASS.

## Fáze K3 – ověření lokálního záznamu

Doplň aktuální privátní RTSP URL a vhodný `record_dir` (může být Synology SMB share). Spusť jediný záznam, ověř MKV přes `ffprobe` a test stažení z UI. Neukládej RTSP URL do Git repozitáře ani protokolu. SD kartu zatím nečti.

**Akceptace:** Přehraje se skutečný HEVC MKV segment, bez nového CS2 spojení ke kameře.

## Fáze K4 – jeden skutečný levý PTZ krok

Převzít přesný aktuální preflight a schválení z `diagnostika/C201_E1_PREDANI_FINAL.md`. Ověřit PID, executable, čas startu, log, DataDir a funkci `managed-ptz-http-probe.exe`. Zapnout `ptz.enabled` a ověřit jedno explicitní kliknutí. Na timeout **neopakovat**. Vyhodnotit zvlášť `0x112`, `0x113`, obrazový posun a fyzický pohyb.

**Akceptace:** Přímé sanitizované důkazy jednotlivých etap; nikdy nezaměňovat HTTP 200 s pohybem.

## Fáze K5 – rozšíření funkcí (nový úkol)

Až po důkazu funkčního PTZ a obrazu. Prozkoumat v konkrétní C201 přístupnost SD kapacity, read-only listing a výběr záznamu. Pokud neexistuje lokální endpoint, navrhnout jeho samostatnou implementaci do provideru s offline testy. Neodesílat neznámé MIoT zápisy, nestrhávat funkce Xiaomi Home naslepo. Později přidat IR, motion, audio a zbývající PTZ směry.

## Důležitá pravidla

- Žádný krok nesmí bez výslovného souhlasu zastavit starou gateway, nasadit novou binárku nebo pohnout motorem.
- Historické PID `38416`, IP `192.168.3.119` a staré porty nejsou aktuální důkaz. Naposledy oznámená IP `192.168.2.124` není nezávisle ověřena.
- Konzole smí běžet jako samostatný proces vedle gateway. Nevyžaduje změnu jejího zdrojového kódu.
- Testy konzole nejsou totéž jako testy reálné C201. Live stav dokládat odpovídajícími runtime výstupy.
