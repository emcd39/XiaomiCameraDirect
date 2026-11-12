# Zadání pro implementačního agenta: normální PTZ ovládání C201

## Výsledek, který uživatel chce

Pracuj v `C:\DEV\xiaomi-local-gateway`. Konzole je v podadresáři `xiaomi-diagnostic-console` (samostatný Go modul).

Uživatel chce kliknout na šipku a otočit testovací kameru C201. Odstraň checkbox o statické scéně, opisování `ONE-LEFT-APPROVED` i potvrzovací dialog před odesláním. Kliknutí samo je požadavkem na pohyb. Uživatelský požadavek výslovně povoluje živé PTZ testování této kamery; neopakuj dotazy na souhlas, které už byly v této konverzaci zodpovězeny.

První dodávka je funkční levá šipka přes existující gateway. Potom dokonči čtyři směrová tlačítka. Diagnostika obrazu nesmí podmiňovat odeslání běžného povelu. Úkol není hotový pouhým `ptz.enabled=true`, mock testem ani odpovědí HTTP 200.

Tento dokument je plán, nikoli záznam provedené implementace. Vznikl po přečtení aktuálních zdrojů. Historické běžící procesy, porty a PID zjisti znovu; nepovažuj čísla ze starých reportů za aktuální.

## Co už existuje a kde je problém

| Soubor | Aktuální chování / potřebná změna |
| --- | --- |
| `xiaomi-diagnostic-console/internal/console/static/index.html` | Obsahuje `phase0`, `confirmation` a tlačítko `ptzLeft`. Odstranit potvrzovací prvky. |
| `xiaomi-diagnostic-console/internal/console/static/app.js` | Handler tlačítka kontroluje checkbox, text a `confirm()`. Odstranit všechny tři kroky. Inventář má také zbytečný potvrzovací dialog. |
| `xiaomi-diagnostic-console/internal/console/server.go` | `ptzLeft` vyžaduje `phase0_confirmed` a `confirmation`. Změnit kontrakt běžného ovládání. |
| `xiaomi-diagnostic-console/internal/console/ptz.go` | `RunOnce` spouští `managed-ptz-http-probe.exe`, nastavuje proměnné souhlasu a čeká až 65 s. Tuto cestu nahradit přímým HTTP voláním gateway. |
| `xiaomi-diagnostic-console/internal/console/gateway.go` | Už má HTTP klienta a čtení Bearer tokenu ze souboru. Inventář nyní ukládá souhrn a první nalezenou RTSP URL, ale nevybírá spolehlivě ID cílové C201. Doplnit konkrétní výběr ID a streamu téže kamery. |
| `internal/httpapi/api.go` | Existuje `POST /v2/cameras/{id}/ptz/step`, JSON `{"direction":"left"}`. `managedStepOnce` zatím přijímá jen `left`. HTTP 200 obsahuje `{"write_status":"sent"}`. |
| `internal/xiaomi/provider.go` | `Provider.Step` také povoluje jen `left`. Používá existující připravenou kamerovou session, zavolá `DiagnosticMotorStepObserved` a zaloguje výsledek. Nevyžaduje obrazovou baseline. |
| `internal/xiaomi/protocol/stream/stream.go` | `motorOperation` již mapuje čtyři směry na pojmenované MISS konstanty. |
| `internal/xiaomi/protocol/miss/client.go` | Konstanty: left=1, right=2, up=3, down=4. Implementace používá jednotlivé kroky `MotorStep`. Platnost fyzického směru na C201 se má ověřit živě. |
| `internal/xiaomi/ptzprobe/` | Diagnostický algoritmus sbírá a posuzuje snímky před zápisem. Nechat jako samostatný diagnostický nástroj, vyřadit jej z běžného kliknutí. |
| `xiaomi-diagnostic-console/scripts/Prepare-Test-Camera.ps1` | PTZ enabled dnes závisí i na SHA helperu, verifieru a jejich existenci. Odstranit tyto závislosti pro přímé ovládání. |

## Rozhodnutí o architektuře

Použij tuto cestu:

`kliknutí → konzolový POST → autentizovaný POST gateway → existující session C201 → MotorStep`

Go už motorický paket posílá a kamera na předchozí pokusy odpovídala `0x113`. Přepis do Pythonu nebo Node by přidal druhou implementaci bez řešení příčiny. Zůstaň u Go; nový jazyk použij jen při konkrétně doložené překážce, nikoli jako první krok.

Odstraň aplikační potvrzování, povinnou statickou scénu, FFmpeg preflight a povinný obrazový důkaz před povelem. Neodstraňuj přihlášení ke gateway ani směrování na konkrétní kameru: jsou to součásti fungující komunikace. Token zůstává na serveru. Browser jej nepotřebuje.

Jedno kliknutí znamená jeden krok. Nevytvářej automatický pohyb po otevření stránky, opakování při držení tlačítka ani retry zápisu po timeoutu. Uživatel může kliknout znovu. To má zabránit duplicitám v aplikaci, nikoli přidat další schvalování.

## 1. Zprovozni přímý levý krok

1. Přečti aktuální konfiguraci a stav gateway bez vypisování tokenu nebo RTSP URL. Zjisti skutečné ID testovací C201 z inventáře. ID neodvozuj z její IP a nepoužívej ukázkové `xiaomi_0000000000000000`.
2. V `gateway.go` ukládej vybrané ID C201 a její RTSP URL z téhož záznamu inventáře. Použij skutečné schéma `/v2/cameras` podle modelů a handlerů v repozitáři. Nezvol první URL ze čtyř různých kamer. Při jediné C201 ji vyber automaticky, při více C201 nabídni konkrétní výběr.
3. Přidej metodu například `Gateway.Step(ctx, cameraID, direction)`. Sestaví JSON, použije stávající klient a token, odešle právě jeden autentizovaný POST na `/v2/cameras/{id}/ptz/step`, přečte omezenou odpověď a vrátí výsledek. Zachovej zákaz přesměrování s tokenem. Použij časový limit odpovídající gateway handleru s malou rezervou, nikoli původních 65 sekund.
4. Přepoj `PTZ.RunOnce` nebo jej nahraď metodou, která volá `Gateway.Step`. `Server` už vlastní `s.gateway`; nepřidávej další oddělenou instanci inventáře. Pro mock vrať simulaci bez jakéhokoliv gateway POST.
5. Zachovej `/api/ptz/left` jako jednoduchý kompatibilní vstup pro první dodávku. Už nevyžaduje historická potvrzovací pole; JS může posílat `{}`. Zachovej POST a stávající kontrolu lokálního původu/action headeru.
6. Z UI odstraň checkbox, textové pole, všechna související čtení DOM a PTZ `confirm()`. Kliknutí rovnou odešle POST. Přidej lokální `ptzPending` po dobu požadavku; pravidelný `status()` jej nesmí přepsat a tlačítko předčasně znovu povolit. Ve `finally` obnov ovládání.
7. Z tlačítka inventáře odstraň potvrzovací dialog. Při běžném startu připrav výběr kamery jednou; nepřidávej opakované inventory GET do pollingu statusu. Na UI ukaž „Připojuji kameru“ při skutečné přípravě session.
8. Pro povel potřebuje gateway připravenou session, nikoli stabilní obraz. Použij existující cestu inventáře/náhledu k navázání session. Pokud ještě není připravena, dokonči její přípravu a až potom proveď jediný POST. Při chybě po odeslání neopakuj stejný POST automaticky.
9. V `Prepare-Test-Camera.ps1` odděl dostupnost PTZ API od helperu/verifieru. Chybějící helper nebo starý hash helperu nesmí blokovat přímé PTZ. Samotný GET s výsledkem 405 je pouze známka route, nikoli důkaz správného těla a směrové podpory.

První milník: nově sestavená konzole po kliknutí odešle levý povel existující gateway bez přepisu gateway a bez analýzy baseline. Neodkládej tento milník kvůli čtyřem směrům, změně designu nebo přepisování diagnostiky.

## 2. Doplň čtyři směry

1. Přidej konzolový endpoint například `POST /api/ptz/step` s `{"direction":"left|right|up|down"}` a čtyři šipky. Původní levý endpoint může delegovat na stejnou implementaci, nesmí poslat druhý povel.
2. V `managedStepOnce` i `Provider.Step` rozšiř validaci o `right`, `up`, `down`. Použij existující `motorOperation`; nevymýšlej jiná command ID nebo payloady.
3. Neodstraňuj synchronizaci session. Zámek v konzoli sám nestačí: mohou běžet dvě konzole na různých portech. Využij existující serializaci na úrovni gateway/provideru, aby se kroky a protokolové odpovědi nepomíchaly.
4. Aktualizuj capability/UI texty. Neoznačuj směr za ověřený na C201, dokud na ní neproběhl odpovídající živý krok.
5. Změna gateway vyžaduje její nový build a kontrolovaný restart pouze projektové testovací instance. Použij existující startér, explicitní FFmpeg a aktuální privátní konfiguraci. Aktualizuj případný SHA pin vlastního nového gateway buildu v přípravě konzole. Neprováděj plošné ukončování podle názvu procesu; původní proces identifikuj podle run metadat, PID, cesty a času startu. Neprováděj restart gateway pro první levý milník, pokud stará gateway již stačí.

## 3. Udrž výsledek ovládání jednoduchý a pravdivý

- Pro běžné ovládání zobraz krátký stav „Odesláno“, „Kamera není připojená“ nebo konkrétní chybu API. Nedělej z běžného tlačítka panel laboratorních podmínek.
- Existující gateway HTTP odpověď potvrzuje `write_status=sent`; neobsahuje samostatný důkaz fyzického pohybu. Nepřeváděj tuto hodnotu na vymyšlené `motion=confirmed` nebo `reply_0x113=received`.
- Odpověď `0x113` lze dohledat v privátním gateway logu pro konkrétní test. V aplikaci může zůstat v rozbalitelné diagnostice. Chybějící měření obrazu nesmí změnit úspěšný zápis na chybu helperu.
- Nevyžaduj FFmpeg pro samotné odeslání PTZ. Náhled a nahrávání jej nadále používají nezávisle.
- Nevymýšlej tlačítko Stop posílající neznámý příkaz: současná cesta je jednotlivý motorický krok. Souvislý pohyb při držení není součástí této dodávky.

## 4. Ověř jen to, co rozhoduje o funkčnosti

1. Konzolový handler/klient: kliknutí bez potvrzovacích polí vede přes falešný gateway transport právě k jednomu POST se správným ID, směrem a autentizací. Mock neposílá nic. Timeout/neúspěch nezpůsobí opakovaný POST. Test nesmí tisknout token.
2. Frontend: headless kliknutí odešle jeden POST a neotevře dialog; polling za běhu požadavku nezpůsobí duplicitní odeslání. Ověř i načtení stránky po odstranění DOM prvků, aby nezůstaly null reference.
3. Pro čtyři směry aktualizuj existující testy HTTP validace a provideru, které dnes odmítají vše kromě `left`. Přidej ověření směrového mapování. Žádný rozsáhlý nový simulátor obrazu.
4. Sestav konzoli přes `scripts/Build-Windows.ps1 -GoPath C:\Apps\go\bin\go.exe`. Gateway sestav pouze po změnách z bodu 2. Pro testy používej správný modul: konzole má svůj vlastní `go.mod`.
5. Spusť nový EXE přes `Start-Test-Camera.cmd`. Obsazený port má použít existující fallback. Otevři skutečně vypsanou URL a ověř, že servíruje nové HTML/JS bez checkboxu a textového pole; starý proces na 18880 může stále servírovat starou verzi.
6. Ověř živý levý krok z nové konzole. Zachyť obraz před a po nebo přímé pozorování kamery; snímky mohou být soukromý testovací artefakt. Zaznamenej čas kliknutí, jeden zápis `0x112`, odpověď `0x113` a výsledek pohybu. Samotné `ret=0` není fyzický důkaz.
7. Je-li pohyb na obrazu zřejmý, neblokuj dokončení tím, že starý algoritmus hledání shod vrací „inconclusive“. Pokud není vidět, prohlédni snímky a protokol konkrétního pokusu a oprav příčinu v session/payloadu. Další krok použij k ověření konkrétní změny, ne jako automatické opakování naslepo. Nepokračuj ve vývoji obrazového algoritmu místo ovladače.
8. Po rozšíření gateway ověř jednotlivě všechny čtyři směry, včetně skutečného směru obrazu. Již ověřený směr nezkoušej pořád znovu. Nepřidávej další potvrzovací otázky, protože uživatel tyto PTZ testy výslovně požaduje.

Pokud omezený pracovní proces neumí loopback nebo DNS, použij dostupný povolený Windows kontext jako v předchozích úspěšných testech. Neopravuj kvůli sandboxové chybě systémový DNS/firewall. Skutečné zamítnutí nástroje nebo nedostupnou síť popiš jako konkrétní překážku, ne jako úspěšný živý test.

## 5. Předání a konec práce

Uprav `README.md`, `START_HERE.txt`, `scripts/Test-Mock.ps1` a relevantní testy podle nového kontraktu. Staré diagnostické nástroje mohou zůstat samostatně. Do `docs/TEST_REPORT.md` přidej stručný nový záznam: build, skutečná URL, vyzkoušené směry, počet odeslaných kroků, odpovědi a pozorovaný pohyb.

Hotovo znamená: uživatel spustí CMD, vidí aktuální konzoli, klikne na šipku bez checkboxu/textu/dialogu a C201 se otočí odpovídajícím směrem. Neúspěšná nebo neověřená část se tak musí jmenovat i v závěrečné zprávě. Nepředávej pouze seznam úspěšných preflight kontrol.

Pracuj postupně a po každém milníku napiš jednu větu o skutečném výsledku. Nezakládej novou architekturu, cloudovou službu ani další schvalovací mechanismus. Neopravuj nesouvisející runbook testy nebo HA integraci. Žádné tvrzení o provedeném review bez skutečného reportu.
