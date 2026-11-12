# Xiaomi Local Gateway – Diagnostic Console

**Samostatná Go webová aplikace** pro lokální diagnostiku a ovládání Xiaomi C201. Obsahuje Windows x64 binárku, zdroje, HTML/CSS/JS bez frontend frameworků, mock režim, RTSP náhled, lokální nahrávání a čtyři jednorázové PTZ kroky přes existující managed gateway.

> **Důležité:** Konzole posílá každý klik jako jeden autentizovaný POST do Xiaomi Local Gateway a používá její připravenou C201 session. Token zůstává na serveru. API zůstává na loopbacku a firewall se nemění. Odpověď gateway potvrzuje `write_status=sent`, sama ale nepotvrzuje fyzický pohyb.

## Nejrychlejší spuštění (bez kamery)

1. Rozbalit celý ZIP, například do `C:\DEV\xiaomi-local-gateway\tools\diagconsole\`.
2. V PowerShellu v rozbalené složce:
   ```powershell
   .\scripts\Start-Demo.ps1
   ```
   Nebo bez PowerShell skriptu:
   ```powershell
   .\bin\diagconsole-current.exe -config .\config.mock.json
   ```
3. Otevřít **http://127.0.0.1:18880**.
4. Kliknout na libovolnou šipku. UI zobrazí simulaci; na gateway ani kameru se nic neposílá.
5. Ukončit `Ctrl+C`.

## Živá testovací C201 ve Windows workspace

Pro přibalenou pracovní kopii v `C:\DEV\xiaomi-local-gateway\xiaomi-diagnostic-console` použij bez argumentů **`Start-Test-Camera.cmd`**. Funguje i při spuštění z jiného pracovního adresáře a nevyžaduje ruční přípravu `config.live.json`.

1. Spouštěč nejprve ověří existující managed testovací gateway podle PID, executable a místního `/healthz`. Pokud žádná ověřená gateway neběží, spustí `.test-env/c201-pilot/win-gateway/start-host-gateway.ps1` s explicitní cestou k FFmpegu. Používá aktuální Windows uživatelský profil Xiaomi Viewer a vyžaduje přístup k Xiaomi účtu, DNS i síti testovací kamery.
2. Gateway bootstrap při vlastním startu obnoví Xiaomi účet a vyžádá inventář. Tento první start může otevřít kamerovou session. Nepředává se `-ExposeApi` ani `-AddScopedFirewall`: API je pouze na loopbacku a pravidla firewallu se nemění.
3. Spouštěč vytvoří ignorovaný `config.live.json` s `mock=false` a spustí konzoli. Dostupnost PTZ určí registrace managed PTZ route; samotný route check neověřuje tělo ani směry. Při volném portu použije `127.0.0.1:18880`; pokud je port obsazený, původní proces ponechá běžet a nová konzole zvolí volný port na loopbacku. Skutečnou URL vypíše až po úspěšném bindu. Otevři ji v prohlížeči.
4. Při načtení stránky se inventář vyžádá jednou a připraví se session jediné nalezené C201. Při více C201 vyber konkrétní kameru. Tento krok může otevřít CS2 session. Náhled zapínej samostatně, pokud chceš sledovat obraz; PTZ ho nevyžaduje. Změna výběru nebo obnova inventáře náhled automaticky zastaví; po připojení kamery ho můžeš znovu spustit. Náhled lze také ukončit tlačítkem **Zastavit** a konzoli pomocí `Ctrl+C`. Gateway běží samostatně.
5. Pokud start selže kvůli Xiaomi DNS/síti nebo profilu, spouštěč zobrazí bezpečnou diagnostickou kategorii a konzoli neotevře. Token ani RTSP URL nekopíruj do chatu či do běžného protokolu.

Tento automatický start je jen pro existující lokální C201 testovací prostředí projektu; není určený pro samostatný ZIP ani pro nasazování jiné/produkční gateway. Pro vlastní připravenou gateway zůstávají dostupné `config.live.example.json` a `scripts/Start-Live.ps1`; v tom případě nastav aktuální hodnoty bezpečně a nech `ptz.enabled=false`.

Při ručním nahrávání vznikají pětiminutové soubory `c201_YYYYMMDD_HHMMSS.mkv` ve zvoleném `record_dir`. Nejde o záznamy na SD kartě kamery.

## Ovládání motoru

Šipky vlevo, vpravo, nahoru a dolů posílají jednotlivé managed kroky. Jedno kliknutí znamená jeden POST; držení tlačítka pohyb neopakuje a klient po timeoutu POST neopakuje. Před prvním povelem se připraví session z inventáře; stabilní obraz ani FFmpeg nejsou podmínkou odeslání.

UI zobrazuje zvlášť HTTP zápis, odpověď `0x113` a obrazový pohyb. Gateway potvrzuje `write_status=sent`; konzole z toho nevyvozuje fyzické otočení. Směr na C201 se označuje jako ověřený až po živém pozorování odpovídajícího pohybu.

## Ostatní funkce

- **SD karta C201:** dosud nemáme prokázané lokální API pro kapacitu, seznam nahrávek ani přehrávání. Panel ukazuje stav `unknown`; nic nečte a neformátuje.
- **Nahrávání do NAS:** funkční mechanika přes FFmpeg segmenty do `record_dir` (může to být připojená SMB share se správnými právy). Ověření proti živé C201 čeká.
- **Detekce pohybu, IR, audio a presety:** zobrazeny v capability exploreru, nejsou označovány jako funkční.
- **Offline nezávislost na cloudu:** samotné vyjednání CS2 může stále potřebovat Xiaomi cloud, zatím neprokázáno bez WAN.

## Ovládací API klienta

| Metoda | Endpoint | Vedlejší efekt |
|---|---|---|
| GET | `/api/status` | Pouze `/healthz`, `/v2/info`, nikdy inventory |
| POST | `/api/inventory/refresh` | Explicitní `/v2/cameras` **může otevřít CS2 session** |
| POST | `/api/inventory/select` | Výběr konkrétní C201 při více kamerách a příprava její session |
| POST | `/api/video/start`, `/api/video/stop` | Jeden lokální FFmpeg decoder přes gateway RTSP |
| GET | `/api/video/mjpeg` | Více prohlížečů sdílí jediného decoder workeru |
| POST | `/api/record/start`, `/api/record/stop` | FFmpeg segmentace RTSP na disk/NAS |
| GET | `/api/record/files` | Čtení seznamu segmentů |
| GET | `/api/record/download/{filename}` | Stažení pouze validovaného záznamu |
| POST | `/api/ptz/left` | Kompatibilní levý krok; přijme `{}` |
| POST | `/api/ptz/step` | Jeden krok pro `camera_id` z karty a směr `left`, `right`, `up` nebo `down` |
| GET | `/api/capabilities` | Deklarované a neověřené schopnosti (bez volání kamery) |
| GET | `/api/events` | Sanitizované lokální události |

Každý POST musí obsahovat hlavičku `X-XLG-Action: 1`. PTZ tělo obsahuje `camera_id` zobrazené C201 a směr; žádné potvrzovací pole se neposílá. Pokud se výběr během přípravy změní, konzole vrátí HTTP 409 a gateway krok neodešle. Kompatibilní `/api/ptz/left` dál přijímá `{}`.

## Technické vlastnosti a bezpečnost

- Go 1.22+ standardní knihovna, HTML/CSS/JS vloženy přímo do EXE (`embed`).
- Pouze loopback HTTP (nepovoluje `0.0.0.0` ani remote gateway adresu). Origin, Host a vlastní hlavička pro akce; nepoužívá cookie ani CORS.
- Bearer token jen ze souboru na serveru; JSON konfigurace je bez hesel. Pro výběr kamery dostane prohlížeč pouze C201 ID a název; RTSP URL zůstává na serveru.
- Při načtení stránky se inventář vyžádá právě jednou, ne v pollingu statusu. PTZ používá připravenou session; náhled a nahrávání zapíná uživatel zvlášť. Nahrávání využije další klientské RTSP připojení ke **gateway**, ne nové CS2 spojení ke kameře.
- FFmpeg dostává RTSP capability URL jako argument procesu: administrátor stejného Windows stroje může číst příkazovou řádku. Udržujte na hostu přiměřená oprávnění, při práci s privátní URL nepoužívejte sdílený účet.
- Nahrávky nejsou šifrované aplikací; `record_dir` musí mít odpovídající NTFS/SMB oprávnění.
- Ovládání je určené **jen pro místní vývojový stroj**, nepro vystavení do internetu či LAN bez další autentizace, TLS a bezpečnostního review.

## Struktura ZIPu

```text
bin/diagconsole-current.exe         Windows amd64 program (CGO off; current launcher target)
cmd/diagconsole/main.go             CLI
internal/console/*.go               Backend, FFmpeg, API, PTZ, testy
internal/console/static/            Webové rozhraní
config.mock.json                    Funkční demo bez kamery
config.live.example.json            Šablona živé konfigurace
scripts/Start-Demo.ps1              Spustí demo
scripts/Start-Live.ps1              Spustí live s ručně připraveným config.live.json
Start-Test-Camera.cmd               Ověří nebo spustí projektovou testovací C201 gateway a otevře konzoli
scripts/Prepare-Test-Camera.ps1      Bezpečně připraví ignorovaný živý config pro tento launcher
scripts/Build-Windows.ps1           Rebuild/test na Windows se zadaným Go
scripts/Smoke-Test.ps1              Základní smoke test localhost API
docs/POSTUP_OVERENI.md              Fázovaný test na skutečné C201
docs/ARCHITEKTURA.md                Architektura, omezení a rozšiřování
docs/CODEX_NAVAZANI.md              Další implementační úkoly
```

## Test po spuštění mock konzole

Pro úplný mock průchod (syntetické snímky, lokální inventory, simulovaný PTZ, žádná kamera):

```powershell
.\scripts\Test-Mock.ps1
```

Před prvním zápisem skript sám zkontroluje `mock=true`; pokud konzole běží v live režimu, okamžitě končí bez změn. Pro čistě read-only kontrolu dostupnosti je `scripts\Smoke-Test.ps1`. Podrobné výsledky build/test jsou v `docs/TEST_REPORT.md`.

K balíčku je přibalen i `references/C201_E1_PREDANI_FINAL_snapshot.md` jako **referenční kopie uživatelem dodaného runbooku**, ne jako spustitelný postup nebo důkaz živého nasazení.
