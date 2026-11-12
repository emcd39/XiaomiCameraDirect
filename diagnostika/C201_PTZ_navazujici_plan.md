# Xiaomi C201 — navazující plán diagnostiky PTZ

Verze 2026-11-10. Podklad: ZAZNAM_VYSLEDKU_C201.md (A–AG), PLAN_C201.md, veřejné zdroje Xiaomi Camera Viewer a go2rtc. Není to popis již provedeného nového testu.

## Cíl

Zachovat funkční přenos C201 `xiaomi.camera.c201` FW `5.3.2_0016`, region `de`, aktuálně uživatelem nahlášená IP `192.168.2.124` (2026-11-10; dosud síťově neověřena), přes CS2/TCP + MISS; zjistit, zda kamera fyzicky reaguje na motorický příkaz, a získat přesnou vazbu `request → response → pohyb` bez vývoje HA integrace. Historická IP `192.168.3.119` zůstává v předchozích měřeních. Neprovádět flash, reset, kalibraci, brute force command ID ani náhodná těla příkazů.

## Známé výsledky / mezery

- Lokální Go gateway obnovila účet a navázala CS2/TCP. Vyjednání session vyžaduje Xiaomi API.
- Video: H.265 848×480, souvislých 300 s, 5937 dekódovaných snímků, 3 reconnecty s dekódováním.
- MISS command channel 0: read-only `0x110` → `0x111` skutečně zodpovězeno.
- `MotorStep` odeslán 1×; nebyl potvrzen fyzický pohyb; přesný request payload a `0x113` odpověď nejsou ve sdílených záznamech.
- Při jednosnímkovém testu v 160×90 byla vysoká změna pixelů, ale nízká spolehlivost odhadu posunu. Není důkazem pohybu.
- Pozdější testy nedošly ke kameře: timeout `account.xiaomi.com` / `de.api.io.mi.com`; některé starší DNS chyby vznikly v omezeném shellu a nejsou důkazem chybného Windows DNS.
- HLS/HA a WAN-offline testy jsou samostatná témata. NEJSOU nyní na kritické cestě PTZ.

## Fáze 0 — fyzická kontrola (nutná před zápisovým PTZ testem)

1. V Xiaomi Home zkontrolovat, že skutečná C201 motoricky reaguje na L/P/U/D. Nastavit kameru přibližně doprostřed obou rozsahů. Nic neotáčet rukou.
2. Dočasně vypnout human/pet tracking a automatické sledování; vypnout živý náhled v Xiaomi Home. Pokud vyžaduje posuv kamery appka, zavřít ji před dalším testem.
3. Zaměřit kameru na statickou scénu s pevnými výraznými body (police, dveře, rám okna), bez lidí, televize či pohybu. Neměnit osvětlení/noční režim.
4. Pokud PTZ není funkční v originální aplikaci, nejdřív opravit základní kameru a nepokračovat v našem zápisovém testu.

## Fáze 1 — jeden spolehlivý proces a trvalá session

1. Spouštět v běžném Windows hostitelském kontextu, nikoli v omezeném Codex sandboxu. Stávající `start-host-gateway.ps1` načítá token z DPAPI profilu Vieweru a nastavuje `XLG_XIAOMI_USER_ID`, `XLG_XIAOMI_REGION`, `XLG_XIAOMI_TOKEN`. Nikdy je neukládat do logu, CLI ani sdíleného souboru.
2. Zachovat ověřený lokální gateway build (SHA-256 z diagnostiky). Načíst inventář jednu C201; otvor `cs2+tcp`, profil `auto`=0, `audio=false`. Žádné pokusy profilů 1–5 nyní.
3. Čekat na `account_restore ok`, `session_negotiate ok`, `session_open`, `first_media`, `rtsp_description ready`. Před `rtsp_description ready` nepouštět FFmpeg, aby nevznikal falešný `503`.
4. Neotevírat během testu další samostatnou CS2 session kvůli PTZ. Příkaz odeslat přes command channel 0 *té samé session*, kterou drží video a RTSP relay.
5. Zajistit stálého RTSP konzumenta po celou dobu testu, aby se médium nevypínalo mezi screenshoty.
6. Při account/inventory/session timeoutu ukončit tento pokus jako `blocked_pre_session`, nelogovat nulové snímky jako selhání kamery; neprovádět PTZ.

## Fáze 2 — přístrojový záznam motorického příkazu

Prohlédnout ve vlastním zdrojovém projektu:
- `internal/xiaomi/protocol/miss/client.go` — typy command ID, implementace `MotorStep`, čekání na `0x113`.
- `internal/xiaomi/protocol/stream/stream.go` — metoda `Session.Raw` / command dispatch, zachování aktuální session.
- `internal/xiaomi/protocol/miss/cs2/conn.go` — channel 0, typ připojení a ukončení.
- `internal/xiaomi/provider.go` — session ownership a PTZ volání.

Nejdříve pouze čtení kódu: zjistit, *co přesně* dřívější `MotorStep` posílá — číslo command ID, přesné JSON/bytes payloadu, channel, čekání na reply, timeout. Pokud to není `0x112` s `{"operation":1}`, nelze starý test považovat za test této varianty. Zachovat poctivé rozlišení `write-ok`, `reply received`, `motor moved`.

Pro diagnostiku logovat pouze: čas, anonymní camera label, spojení CS2+TCP, `cmd_sent_id`, canonical sanitized payload (`{"operation":N}`), `reply_cmd_id` a délku, zda byl `0x113` dekódován, pole `ret/angle/elevation/xAngle/yAngle/xStep/yStep` jsou-li v odpovědi, latency, výsledný pohyb. Nezaznamenávat DID, session URL, tokeny, klíče, surové šifrované materiály.

Známé vzory Z JINÝCH MODELŮ (nepotvrzeno pro C201):
- MISS `0x112`, tělo `{"operation":1}` levá, `2` pravá, `3` horní, `4` dolní; odpověď typicky `0x113`.
- Někde je `0x112` s různým tělem tichý no-op a reply nemusí znamenat pohyb.
- U jiného modelu `operation=6` čte pozici a `operation=13` nastavuje absolutní cíl; pro C201 je nepoužívat bez nezávislého potvrzení, že mají stejnou sémantiku.
- Varianty `dafangRaw(0xff2404,...)` patří starším TUTK/Dafang modelům; neposílat je na C201 naslepo.

## Fáze 3 — průkazný fyzický test (maximálně jeden zápisový krok do získání jasného výsledku)

1. Při běžícím CS2 video session počkat na ustálený H.265 stream a několik dekódovaných keyframů; používat alespoň 640×360 (raději nativních 848×480) a `audio=false`.
2. Držet jediný kontinuální dekodér, vybrat 8–10 časově označených snímků v klidovém okně během 3–5 s bez příkazu. Změřit no-move drift (shodu statických bodů). Jeden jediný 160×90 snímek je nedostatečný.
3. Pak v existující session odeslat *jeden jediný* známý krok `0x112` `{"operation":1}` (a pouze pokud je potvrzená skutečná implementace a připravená vizuální kontrola). Neposílat držení klávesy ani cyklus. Motor musí mít volný prostor.
4. Zaznamenat odpověď `0x113` a stav command channelu, pokud do 2 s nepřijde, označit `no_reply`, nikoli automaticky failed/no move.
5. Stejným kontinuálním dekodérem snímat během 4–6 s a potom další statické okno. Neotvírat novou RTSP session pro snímek „po“. Přiložit čas `command_sent` a `response_received`.
6. Porovnat statické prvky robustně (např. ORB+RANSAC/phase correlation) a vedle toho ruční vizuální potvrzení fyzického otočení hlavy. Zohlednit posun obrazu v opačném směru než otočení hlavy. Nehodnotit jen pixelový rozdíl.
7. Po potvrzeném pohybu lze ve zvláštním testu ověřit opačný *jediný* krok, poté další osu. Jinak nepokračovat v posílání řady variant.
8. Volitelně natočit samotnou kameru externím telefonem, aby bylo jednoznačné, zda se mechanicky otočila; video z C201 může být ovlivněné overlayem, frame bufferem nebo stabilizací.

## Fáze 4 — vyhodnocovací matice

| Výsledek | Význam | Další krok |
|---|---|---|
| `0x113` přijat, fyzický pohyb potvrzen | PTZ je nalezeno | Ověřit mapu 1–4 a případné pozice, pak implementace ovladače |
| `0x113` přijat, obraz beze změny, žádný fyzický pohyb | Payload neplatný, motor busy, limit nebo firmware rozdíl; ACK nestačí | Zkontrolovat `ret` a ostatní reply pole, zavřít Xiaomi Home a zkontrolovat výchozí polohu |
| `0x113` přijat, video se mění, ale neprokázán posun statických bodů | Neprůkazný test (aktuální stav) | Více snímků, vyšší rozlišení a externí vizuální evidence |
| Žádná `0x113`, ale video pokračuje | Neznámá request/response sémantika, omezený receive dispatch | Zkontrolovat filtr command replies, log commandů, timeouty |
| Příkaz byl zapsán, ale command channel neověřen | `write-ok` není úspěch PTZ | Doplnit příjem odpovědi a korelaci |
| Session končí při Xiaomi account API | Kamera ani PTZ se neotestovaly | Opravit dostupnost přihlášení; jeden čistý hostitelský pokus |
| Video vypadne při PTZ | Možný problém se sdílením session/re-entrant send | Jediná session, bez paralelního PTZ spojení, trace lifecycle |

## Fáze 5 — až pokud krok 1 nefunguje

1. Potvrdit motoriku v Xiaomi Home a odlišit `0x113` jako status vs potvrzení akce.
2. Projít `scripts/probe-ptz.ps1` a implementaci `0x112` v Xiaomi Camera Viewer, dále go2rtc PR #2531 a issue #2162. Pro jiné modely se potvrzuje `{"operation":N}`; nelze to automaticky přenést na C201.
3. Zjistit přesný formát PTZ ze skutečného oficiálního pluginu pro C201 `4560.8.1-8` nebo z diagnostických logů Xiaomi Home při kontrolovaném jednom pohybu. Wireshark na PC samotný neuvidí dešifrovaný příkaz z telefonu; čistě šifrovaný PCAP neodhalí JSON.
4. Pokud není dostupný ověřený příkaz, nespouštět brute-force čísel command ID / nezdokumentované payloady. Předložit nalezený handler/serializaci k porovnání s naším testem.

## Akceptační důkazy / výstup

- `PTZ-01.json`: datum, firmware, software/build, fáze session, přesný sanitizovaný motor request, reply ID a decoded fields, časová osa.
- `PTZ-01-motion.md`: screenshot kontrolní scény, bazální drift, před/po obrazy a odhad posunu, fyzické potvrzení uživatelem (ano/ne/neprůkazné).
- `PTZ-01-conclusion.md`: jednoznačná kategorie `confirmed`, `inconclusive`, `no_effect_with_reply`, `no_reply` nebo `blocked_before_session`; další jeden doporučený experiment.
- Uživatelské autentizační materiály, DID a živé RTSP URL zůstávají pouze lokálně. Nezapínat HA HLS a nedělat offline WAN test, dokud není PTZ diagnostika prokazatelná.

## Externí reference

- https://github.com/lubos-hnanicek/xiaomi-camera-viewer (Pan and tilt; `scripts/probe-ptz.ps1`)
- https://github.com/AlexxIT/go2rtc/issues/2162 (rozdíly mezi kamerami; odpověď `0x113`; pozice)
- https://github.com/AlexxIT/go2rtc/pull/2531 (PTZ přes stávající stream/session; návrh ONVIF)
- https://github.com/AlexxIT/go2rtc/issues/2290 (C201 podpora otevřena)
- https://home.miot-spec.com/spec/xiaomi.camera.c201 (MIoT mapa: kalibrace PTZ != pohyb)

## Stav pokračování (aktualizováno 2026-11-07 po offline ověření)

- Fáze 2 – kontrola zdroje a offline instrumentace – je hotová. `Session.DiagnosticMotorStep(direction)` používá existující session, zaznamená write a časově označenou decoded reply; timeout vynutí `no_reply` pro přijetí po 2 s i při pozdním dequeue. `Provider.Step()` i headless `ptz-once.go` tisknou sanitizovanou časovou osu z téže video session. Testy pokryly včasnou i opožděnou reply, samotný Session s falešným senderem i headless routing s falešnou session; Provider prošel compile-only ověřením. Živý PTZ příkaz se neposlal. Výsledky jsou v `ZAZNAM_VYSLEDKU_C201.md`, částech V–X.
- Jeden běh fáze 1 s ověřenou binárkou skončil při `account_restore` s kategorií `http_request`; inventář, CS2 session a RTSP se neotevřely. FFmpeg snímky 0, PTZ 0. Další běh se v této návaznosti nespouštěl.
- Fáze 0 není potvrzená: chybí potvrzení pohybu v Xiaomi Home, vypnutí sledování a příprava statické scény. Bez toho a bez připraveného stabilního RTSP dekodéru neposílat další zápisový krok.
- Až bude fáze 0 splněna a Xiaomi account restore opět dostupný, pokračovat jednou stejnou session, nejdřív 8–10 baseline snímků, potom nejvýše jeden `0x112` `{"operation":1}`; vyhodnotit `0x113` a shodu více pevných bodů proti baseline driftu.
- Při živém pokusu vytisknout `session.DiagnosticMotorStep("left").SanitizedLine()` ze stejné `stream.Session`, která dodává kontinuální video; samotný status `reply_received` nepovažovat za potvrzení pohybu.
- **Doplnění po časovaném offline ověření (2026-11-07):** celý 60s deadline začíná před `account.Restore` a zahrnuje account restore, inventory, session negotiation/open i samotné měření; po každé cloud/session fázi se kontroluje před pokračováním. MIoT čtení pozice používá context-aware `MiotGetContext`. Po 8 snímcích s intervalem 450 ms se ověřuje skutečný rozsah podle jejich časových značek (3–5 s). Po čtení pozice se před PTZ vyžaduje novější než 1s snímek, který stále odpovídá stabilní baseline; timeout, konec streamu nebo změna scény znamenají nula zápisů. Callback po úspěšném zápisu spustí sběr ihned a souběžně s čekáním na `0x113`; pohybové okno je 5 s. Následuje samostatné osmísnímkové klidové okno s časovým rozpětím 3–5 s. Záznam samostatně uvádí `0x113`, MIoT pozici, obraz v pohybovém okně a posun ve klidovém okně. Cílené simulované testy i kompilace helperu prošly; žádná kamera ani PTZ se nespustily. Fáze 0 zůstává nepotvrzená. Podrobnosti: `ZAZNAM_VYSLEDKU_C201.md`, sekce Z.

## Aktuální výsledek živého pokusu (2026-11-07)

Uživatel potvrdil fyzickou fázi 0. Jediný hostitelský běh obnovil účet, našel jednu C201 a otevřel CS2/TCP; dekodér však nedostal první snímek do limitu 1,5 s. Běh skončil před baseline a před jakýmkoli zápisem: PTZ=0, 0x113=N/A, obrazový pohyb neověřený. Celkový čas byl 19,80 s, timeout sondy začal před account_restore. Sanitizovaný záznam a časová osa jsou v diagnostika/ZAZNAM_VYSLEDKU_C201.md, sekce AA.

## Diagnostika prvního snímku (aktualizace 2026-11-08)

- **Novější stav po pokračování přes existující gateway:** viz sekci AE záznamu. Byla nalezena již běžící C201 instance s odpovídajícím tokenem; launcher se nespustil. Jedna C201 měla stream v aktuálním stavu streaming a kontinuální RTSP/TCP odběr získal 8 snímků 848×480. Baseline je stabilní: 7 párů, minimálně 171 bodů, nejhorší výchozí posun 0 px během 3016 ms. Zápis zůstává 0, protože managed API nezpřístupňuje Provider.Step na existující CS2 session; statický /v1 PTZ endpoint používá ONVIF a není náhradou. Starší poznámka o chybějícím tokenu pro PID 26836 se na aktivní C201 instanci nevztahuje.
- **Připravená cesta:** Provider.Step používá source.session a deleguje do DiagnosticMotorStepObserved(direction, nil), ale běžící managed gateway nemá dostupnou CS2 PTZ API route. Bez existujícího in-process způsobu, jak zavolat metodu na PID 38416, by samostatný helper založil druhou CS2 session. Neprovádět jej. Fáze 0 byla uživatelem naposledy potvrzena 2026-11-07 a její aktuální platnost je nutné potvrdit před případným zápisem.
- Výsledek AA zůstává `blocked_before_write`: `session_open=cs2+tcp` uspěl, ale během tehdejšího 1,5s okna vzniklo 0 dekódovaných snímků a 0 PTZ zápisů. Bez čítačů z tohoto běhu nelze určit, zda chyběly media access units, VPS/SPS/PPS/keyframe, nebo FFmpeg dekódování selhalo.
- **Read-only pokus 2026-11-08:** po výslovném pokynu uživatele se spustil právě jeden připravený helper ve Windows PowerShell procesu. Původní `exec_command` neměl nastavený sandbox override a běžel v režimu `use_default` podle omezení `workspace-write`; samotné `-ExecutionPolicy Bypass` sandbox nemění. Režim mimo omezený sandbox proto není doložen a je označen jako neověřený. Helper skončil při `account_restore` s `reason=dns`, před inventářem, CS2 vyjednáním a otevřením video session. `FIRST_FRAME`, oba pre/post-close snapshoty a všechny media/HEVC/FFmpeg čítače jsou N/A, protože dekodér nevznikl; nejde o nulu paketů. `HELPER_EXIT=0` značí očekávaný časný návrat helperu, ne úspěšné video. Důkaz výsledku: `.test-env/evidence/c201-first-frame-readonly-20261108-075005-a49c7585.log`.
- **Nový offline výsledek:** validní syntetický H.265 848×480 test s kontinuálním přísunem po 40 ms vydal první kompletní frame před EOF po 24 media packets / 639 528 B FFmpeg stdin, za 962 ms v celé suite a 955 ms v posledním cíleném běhu; FFmpeg zůstal ve stavu `running`, protože vstup zůstával otevřený. Při posledním snapshotu měl stdout 439 808 B: jeden celý frame a 32 768 B dalšího frame. Test s fake stdoutem při 50ms timeoutu vykázal přesných 16 384 načtených bajtů, `first_frame_latency_ms=-1` a žádný celý frame; po zavření roury skončil `io.ErrUnexpectedEOF`. Test timeoutu dále odděluje snapshot před `video.Close()` od konečného `context_cancelled/1` po něm. Windows helper byl znovu sestaven pro `windows/amd64`; limity 10 s/60 s a `PTZ_WRITE_ENABLED=false` zůstaly zachovány. Podrobnosti a sanitizované čítače: `diagnostika/ZAZNAM_VYSLEDKU_C201.md`, sekce AB, a `.test-env/evidence/c201-live-video-offline-20261108.log`.
- Helper `.test-env/c201-pilot/win-gateway/first-frame-readonly.exe` je read-only: jedna CS2/TCP session, `quality=auto`, `audio=false`, limit 10 s pro první snímek a celkový deadline 60 s od začátku account restore. Má `PTZ_WRITE_ENABLED=false` a žádný PTZ adapter ani zápis. Před/po-close statistiky jsou oddělené; Windows helper byl sestaven a spustil se právě jednou. Runner zachytil pouze allowlist výstupu.
- Offline test se čtyřmi validními syntetickými access units a 750ms oknem doložil snapshot před úklidem (`running`, `stdout=0`, latence `-1`, exit code `-1`) a zvlášť konečný stav po `video.Close()` (`context_cancelled`, exit code `1`). Sanitizační filtr zachoval oba prefixed snapshoty a zahodil nesouvisející řádek. Podrobnosti jsou v sekci AB záznamu a v offline logu.
- **Doporučené další rozhodnutí:** protože přímá cesta skončila před otevřením streamu, další vizuální ověření vést přes již fungující RTSP gateway; přímý helper neopakovat automaticky. V tomto běhu nebyl odeslán PTZ příkaz a fyzický pohyb zůstává neověřený.
- **Kontrola existující gateway (2026-11-08):** hostitelský DNS/TCP 443 pro Xiaomi a dešifrování stávajícího DPAPI profilu fungují; to samo nedokládá nový cloud account restore. Běžící `xlg` API i RTSP listener jsou dosažitelné, ale `GET /v2/info` bez Bearer tokenu vrací 401. Soukromá složka aktivního procesu token neobsahuje a nelze jej bezpečně přiřadit z jiných historických běhů. Dostupný historický log končí `session_open reason=timeout`; `first_media`, `rtsp_description` ani baseline nyní nejsou doložené. Nedošlo k vytvoření session/dekodéru ani k PTZ zápisu. Další nutný vstup je bezpečný přístup ke správnému API tokenu právě běžící gateway, bez vložení hodnoty do chatu; úplný sanitizovaný rozpad fází je v sekci AD záznamu výsledků.
- Kódový PTZ vstup je `Provider.Step`: po `source.ensureReady` bere právě `source.session` a volá wrapper `Session.DiagnosticMotorStep`, který deleguje do již implementovaného `DiagnosticMotorStepObserved(direction, nil)`. Tím se nepoužívá druhá kamera session; observer callback však není připojen. Žádný motorický zápis neodesílat bez stabilní baseline ze stejné stream relace a následného samostatného schválení.
