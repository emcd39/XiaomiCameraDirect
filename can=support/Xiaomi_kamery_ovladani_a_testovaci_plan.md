# Xiaomi kamery – ovládání, protokoly a ověřovací plán

C201 jako referenční model; rozšíření na další Xiaomi kamery.

Verze 1.0  |  Stav zdrojů k 7. 11. 2026  |  Účel: nejprve prokázat ovládání na živém zařízení, teprve potom implementovat Home Assistant/Frigate.

Tento dokument kombinuje: (A) uživatelem poskytnuté diagnostické záznamy C201, (B) veřejnou dokumentaci a issue/PR open-source projektů a (C) navržené testy, které dosud NEPROBĚHLY.

# Rychlý závěr

- C201: funguje CS2/TCP + MISS, lokální H.265 stream přes gateway, 300 s dekódování a tři RTSP reconnecty. Obousměrný read-only command channel 0x110 → 0x111 je prokázán. Fyzický PTZ pohyb potvrzen není.

- Moderní CW400 a CW500: v projektu Xiaomi Camera Viewer je hardwarově ověřen MISS 0x112 s tělem {"operation": N}; pro ostatní modely nelze automaticky předpokládat stejnou interpretaci.

- Starší Dafang/Xiaofang: jiná větev příkazů (legacy/TUTK a dafangRaw); na moderní C201 je netestovat naslepo.

- Další práce: jeden vybraný model, jedna session, jeden řízený PTZ krok, dekódovaná odpověď a fyzický důkaz pohybu; poté další směry a ostatní kamery.

## Legenda ověření

| Značka | Význam |
| --- | --- |

| POTVRZENO / HW | Test nebo měření na konkrétním zařízení s popsaným firmwarem. |

| PUBLIKOVÁNO | Veřejný projekt nebo issue uvádí experiment na jiném zařízení. |

| KANDIDÁT | Vzor podobného modelu; u naší kamery dosud netestováno. |

| NEZNÁMÉ | Nedostatek údajů, nelze tvrdit podporu ani nepodporu. |




---

# 1. Výchozí stav – naše C201

| Parametr | Naměřený stav / hranice důkazu |
| --- | --- |

| Model / firmware | xiaomi.camera.c201 / 5.3.2_0016; Xiaomi Home region EU (de); plugin 4560.8.1-8. |

| LAN | Kamera 192.168.3.119; PC 192.168.3.178/24; přímo dostupný soused, ping odpovídá. |

| Transport | Xiaomi Cloud vydá MISS materiál session; LAN search UDP 32108; vyjednáno cs2+tcp; nikoli nativní RTSP na kameře. |

| Video | H.265 848×480; FFmpeg 5 937 dekódovaných snímků / 300 s; 3 nová RTSP připojení funkční. |

| Příkazový kanál | MISS 0x110 (DeviceInfo) → 0x111, odpověď 71 bajtů; obousměrný channel 0 potvrzen. |

| PTZ | Dříve vyslán jediný MotorStep. Dvojice 160×90 snímků se změnila, ale posun pevných bodů nebyl spolehlivý. Fyzický pohyb NEOVĚŘEN. |

| Neúspěšné pokusy | Některé následující testy skončily při Xiaomi account restore nebo device list/session API. Nejsou testem motoru. |

| HA / offline | C201 dosud nebyla ověřena jako HA camera entita; WAN-offline nová session neotestována. |



Podklad: přiložený ZAZNAM_VYSLEDKU_C201(1).md, zejména části C–U; START_HERE.txt; C201_PTZ_navazujici_plan.md. Zde uvedené časy a výsledky nejsou novým měřením.

# 2. Architektura, kterou už máme

```
Xiaomi API (účet + klíče) ---- pouze příprava relace ----┐
                                                     ▼
Xiaomi kamera  <---- CS2/TCP + šifrovaný MISS ----> Go gateway
      |                     |                       |
      |                command channel 0            +-- RTSP (lokální výstup)
      |                media channel 2              +-- diagnostické API
      └-- motor / video / audio / nastavení          └-- FFmpeg / Viewer
```

Přenos média probíhá v LAN, ale nová session v dosud používaném klientovi potřebuje cloudové vyjednání klíčů. Lokální RTSP URL patří gateway, nikoli přímo C201. Při testech motoru držet jednu existující session; druhé souběžné P2P připojení může rušit video.

# 3. Typy ovládání, které musíme rozlišovat

| Rovina | K čemu slouží | Co ověřovat |
| --- | --- | --- |

| MISS command | PTZ, informace o zařízení, řízení média | ID příkazu, tělo, odpověď, fyzický efekt |

| MIoT vlastnosti / akce | Noční režim, tracking, záznam, detekce, restart | siid/piid/aiid, rozsah hodnot, cloud vs LAN |

| RTSP / WebRTC gateway | Sledování obrazu a zvuku | Funkční dekódování, nikoli řídicí protokol kamery |

| ONVIF proxy | Standardní obal pro klienty jako NVR/Frigate | Zda je proxy skutečně implementována a vystavuje PTZ |

| Legacy IOCtrl | Starší Dafang/Xiaofang/TUTK | Modelová větev, bezpečný STOP a oddělené testy |



# 4. Přehled modelových větví

Obchodní název „Mi 360°“, „C400“ nebo „Outdoor“ nemusí jednoznačně určovat protocol/firmware. Rozhodující je přesné model ID z inventáře Xiaomi účtu (např. xiaomi.camera.c201, isa.camera.500dh).

| Model / větev | Co je publikováno / ověřeno | První test PTZ |
| --- | --- | --- |

| C201 / xiaomi.camera.c201 | Naše HW: CS2/TCP + MISS video a příkazový kanál; pohyb nepotvrzen. | Zkontrolovat existující MotorStep a odpověď 0x113; jeden krok. |

| C200 / přesné ID zjistit | Komunita go2rtc uvádí funkční stream pro C200; nezaručuje PTZ. | Získat model+vendor, read-only záznam, až potom payload. |

| C300 / více variant | go2rtc komunita má C300 PTZ pokusy; konkrétní firmware rozhoduje. | Stejná identifikace; variantu 0x112 ověřit jen po read-only. |

| C400 / ID zjistit | Xiaomi potvrzuje PTZ v Mi Home; komunita uvádí C400 PTZ přes fork go2rtc. | Přesné ID, firmware, transport; otestovat existující fork. |

| CW300 EU / mxiang.camera.moc006 | Viewer: H.265/Opus video potvrzeno; 0x112 PTZ zatím bez HW důkazu. | probe-ptz na skutečné EU verzi. |

| CW300 CN / mxiang.camera.moc001 | Viewer rozpoznává, ale nebyl proveden HW sweep. | Nejdřív media + MIoT mapa CN; PTZ až poté. |

| CW400 / isa.camera.hlc8a | Viewer: MISS 0x112 + operation N PTZ fyzicky ověřeno. | Regresní test 4 směrů, kontrola odpovědí. |

| CW500 Dual / isa.camera.500dh | Viewer: MISS motor krok, dvě čočky v jedné session; pozor na pouze pevnou druhou čočku. | PTZ motorizované čočky; sdílet session s videem. |

| CW700S / isa.camera.700sa | Viewer: publikovaná MIoT mapa, transport CS2; HW PTZ a hybrid zoom neověřeny. | PTZ až po potvrzení session; zoom bez známého payloadu neposílat. |

| Mi 360° / Outdoor (neurčeno) | Bez přesného model ID nelze určit CS2, TUTK, motorický protokol ani počet kanálů. | První úkol: inventář, vendor, diagnostika bez zápisu. |

| Dafang / Xiaofang starší | go2rtc: legacy motorické IOCtrl, jiné než 0x112 + JSON. | Jen v legacy větvi a s připraveným STOP. |



CW400 není totéž co C400. Stejně tak různá zařízení s „C300“ v názvu nemusejí sdílet model ID ani řídicí kód.


---

# 5. Protokolové příkazy – známé a kandidátní

| Příkaz / směr | Účel | Ověření / poznámka |
| --- | --- | --- |

| 0x100 | MISS autentizační zpráva / envelope podle implementace | Naše C201 logy: v toku relace, nenahrazovat novým klientem. |

| 0x102 | Start videa po navázání session | Naše C201: následuje H.265 stream; start nemá v logu potvrzené samostatné ACK. |

| 0x110 → 0x111 | DeviceInfo request / response | C201 POTVRZENO, 71 B; read-only, bez motoru. |

| 0x112 → 0x113 | Motor request / response | CW400/CW500 publikováno jako fungující PTZ. C201 zatím KANDIDÁT. |

| operation 1–4 | 1 vlevo; 2 vpravo; 3 nahoru; 4 dolů | Z jiných moderních kamer, ověřovat osy a fyzickou reakci. |

| operation 6 | Dotaz na pozici | HW ověřeno pro xiaomi.camera.c01a01; NE automaticky pro C201. |

| operation 13 | Absolutní poloha | HW ověřeno pro xiaomi.camera.c01a01; NEPOSÍLAT bez identifikace modelu. |

| dafangRaw(0xff2404, h, v, speed) | Legacy motorické příkazy | Dafang/Xiaofang; jiné transportní a bezpečnostní předpoklady. |



## 5.1 Kandidát pro moderní CW400 / CW500

```
MISS request 0x112
{"operation":1}   # krok vlevo
{"operation":2}   # krok vpravo
{"operation":3}   # krok nahoru
{"operation":4}   # krok dolu
MISS reply (pokud model vrací): 0x113
```

Jeden krok je samostavicí; opakování simuluje držení šipky. U C201 se nejprve musí přečíst stávající implementace MotorStep. Nepovažovat úspěch zápisu za úspěch příkazu ani za pohyb. Typ odpovědi a význam ret/angle/elevation může být modelově specifický.

## 5.2 Legacy příkazy nejsou přenositelné

```
// Příklad z veřejného go2rtc issue pro Dafang/Xiaofang:
// dafangRaw(0xff2404, horizontal, vertical, speed)
// left=(2,0,5), right=(1,0,5), up=(0,2,5), down=(0,1,5)
// stop=(0,0,5); speed zhruba 1..9 podle konkrétní implementace.
```

Na starých modelech je nutné ukončit pohyb explicitním STOP podle jejich protokolu; nevkládat tyto příkazy do C201/CW400. Hodnoty jsou popsány jako příklady ve veřejném issue go2rtc, nikoli jako univerzální API.

## 5.3 MIoT C201 – bezpečné první kontroly

| Vlastnost / akce | ID | Co lze ověřit |
| --- | --- | --- |

| Power | siid 2, piid 1 | R/W stav; pozor, vypnutí může přerušit test. |

| Noční režim | 2 / 3 | 0 vyp, 1 zap, 2 automaticky; zapisovat až po záloze původní hodnoty. |

| Otočení obrazu | 2 / 4 | 0/90/180/270; změna snímku není PTZ. |

| Časová značka | 2 / 5 | R/W, lze bezpečně ověřit při návratu původní hodnoty. |

| Sledování člověka | 2 / 9 | R/W; před PTZ testem doporučeno vypnout. |

| Režim záznamu | 2 / 11 | Nevypínat stávající záznam bez souhlasu. |

| PTZ kalibrace | siid 2, aiid 1 | Kalibrace != směrový motorický krok; v této fázi nespouštět. |

| Restart | siid 2, aiid 2 | Nevykonávat při live diagnostice. |

| Detekce pohybu | siid 5, piid 1 | R/W, dostupnost lokálně neprokázána. |



MIoT ID vychází z veřejné specifikace xiaomi.camera.c201; jiný model může mít na stejných ID úplně jiné funkce. „R/W“ ve specifikaci není důkaz provozu bez Xiaomi Cloud.


---

# 6. Postup pro každou novou kameru – 8 fází

## F0 – Inventura a referenční test v Xiaomi Home

1. Zapsat obchodní název, přesné MIoT model ID, firmware, region, plugin verzi, IP, MAC jen do neveřejného inventáře.

2. V Xiaomi Home ověřit live video a pohyb šipek v obou osách; u pevné kamery zaznamenat „bez PTZ motoru“.

3. Zaznamenat, zda má druhou čočku, mikrofon, reproduktor, SD, sledování osob/zvířat a noční režimy.

4. Kameru nastavit přibližně do středu rozsahu; vypnout tracking, zavřít Mi Home živý náhled.

## F1 – Read-only zjištění protokolu

1. Spustit Viewer -> Help -> Camera diagnostics -> Collect diagnostics; vybrat konkrétní kameru.

2. Zapsat vendor: CS2/TCP, CS2/UDP, TUTK, MTP, Agora nebo neznámý; handshake stage a media/command frames.

3. Dohledat read-only MIoT mapu pro přesné model ID; nevyužívat mapu podobné kamery.

4. Případné 0x110/0x111 DeviceInfo interpretovat jako důkaz command kanálu, ne PTZ.

## F2 – Video v jediné session

1. Nejprve vyzkoušet výchozí profil, u C201 auto/0; neměnit najednou transport a kvalitu.

2. Počkat na session_open, first_media a rtsp_description ready; časné RTSP DESCRIBE může vrátit 503.

3. Dekódovat alespoň 30–60 s se skutečnými snímky, ověřit codec/fps/rozlišení a po stabilizaci test prodloužit na 300 s.

4. Teprve po úspěchu přidat jedno nové RTSP připojení; nepřipojovat druhou CS2 P2P session kvůli PTZ.

## F3 – PTZ: jeden řízený pohyb

1. Získat 8–10 snímků statické scény v nejméně 640×360 (ideálně přirozený stream) během 3–5 s; změřit baseline drift.

2. Prověřit source code MotorStep: příkaz ID, JSON/bytes payload, délka, channel, timeout, korelace odpovědi.

3. Jen pokud formát odpovídá konkrétní modelové větvi, odeslat jeden krátký bezpečný krok do volného směru.

4. Logovat cmd_sent, reply_cmd, parse result, ret a latenci. Snímat kontinuálně dalších 4–6 s ve stejné session.

5. Fyzicky sledovat hlavici kamerou telefonu nebo přímo očima a současně odhadnout posun pevných bodů obrazu.

6. Při „reply bez pohybu“ test nepovažovat za úspěšný; nejprve zkontrolovat krajní polohu, souběžný Mi Home a formát payloadu.

## F4 – Kompletní mapa PTZ až po prvním úspěchu

1. Otestovat opačný směr a návrat; pak samostatně druhou osu. Vždy jeden příkaz a vizuální důkaz.

2. Zjistit, zda model používá kroky, držení tlačítka, STOP, odpověď polohy, absolutní polohování či presety.

3. Ověřit krajní polohy bez nárazů; nepouštět automatický sweeping, kalibraci ani operation 13 bez modelově ověřené sémantiky.

## F5 – Ostatní nastavení / události

1. Číst siid/piid a původní hodnotu. Měnit vždy jen jednu uživatelsky vratnou vlastnost (např. night shot).

2. Ověřit změnu fyzicky nebo v Xiaomi Home; vrátit původní hodnotu.

3. Detekční eventy zkoušet pouze s časovými značkami a sledovat, zda chodí do LAN, nebo jen přes cloud.

4. Audio poslouchat odděleně od talkbacku; jejich podpora a kodeky jsou nezávislé.

## F6 – Offline matice (až po stabilním ovládání)

Nová relace s WAN povoleno oběma; pak vypnout WAN jen kameře; jen gateway; oběma; a po restartu obou. Nikdy neblokovat LAN/DHCP. Pouhý pokračující stream nepředstavuje offline autentizaci.

## F7 – Akceptace před HA integrací

Minimálně: jednoznačné model ID; stabilní video, fyzicky ověřené všechny dostupné PTZ směry; správně dekódované odpovědi, zvuk dle potřeby; opakovatelnost po restartu; změřená závislost na WAN; logy bez tokenů. Teprve potom adapter pro HA/Frigate.


---

# 7. Praktické nástroje a příkazy

## 7.1 Viewer – profil a diagnostika

```
# Ve staženém a rozbaleném Xiaomi Camera Viewer:
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\collect-camera-diagnostics.ps1
# GUI alternativa: Help > Camera diagnostics > Collect diagnostics
```

Uložit summary.json, communication.json a miot-spec.json (po sanitizaci) plus „owner-report“. Nezveřejňovat DID, cloud tokeny, odpovědi s přihlašovacími údaji, RTSP session URL a klíče.

## 7.2 go2rtc – video test, nikoli PTZ automaticky

```
# Spustit portable go2rtc na Windows a otevřít:
http://127.0.0.1:1984
# Add > Xiaomi > účet/region > zvolit kameru; název streamu např. c201
# Následně (jen pokud tento stream existuje):
ffprobe -v error -show_entries stream=codec_name,width,height,avg_frame_rate -of json rtsp://127.0.0.1:8554/c201
ffmpeg -hide_banner -loglevel warning -rtsp_transport tcp -i rtsp://127.0.0.1:8554/c201 -an -t 30 -f null NUL
```

Pokud běží vlastní gateway na dynamickém portu, použít její aktuální skutečnou RTSP URL; předchozí endpoint 192.168.3.178:53538 byl dočasný a není aktivní. Nesdílet živou session URL.

## 7.3 Read-only analýza Windows / síť

```
Test-NetConnection 192.168.3.119 -Port 554
# Zavřený 554 není důkaz, že Xiaomi neumí LAN video.
# Wireshark display filter (na stejné síti):
ip.addr == 192.168.3.119
# Volitelně sledovat discovery:
udp.port == 32108
```

Záznam v Wiresharku z PC není automaticky dekódováním povelů z telefonu; šifrované payloady neuvidíme v čitelné podobě. Pro kontrolu vlastní CS2 session stačí kombinovat paketovou stopu s bezpečným aplikačním logem.

## 7.4 Log události testovacího runneru

```
{"event":"session_open","model":"xiaomi.camera.c201","transport":"cs2+tcp","result":"ok"}
{"event":"first_media","codec":"h265","width":848,"height":480}
{"event":"ptz_send","command":"0x112","payload":{"operation":1},"session_reused":true}
{"event":"ptz_reply","command":"0x113","received":true,"ret":null,"latency_ms":null}
{"event":"ptz_motion","method":"visual+scene_match","confirmed":false,"reason":"not_yet_measured"}
```

Tyto JSON řádky jsou PŘÍKLAD cílového schématu logu, nikoli skutečný výstup C201. Nezaměňovat placeholdery s naměřenými hodnotami.

## 7.5 Není-li PTZ, ale video ano

- Video gateway neodstraňovat. L2 je hotové; prověřovat pouze command path.

- Zkontrolovat, zda nemá model PTZ přes MIoT, MISS, legacy IOCtrl či proprietární prostředí aplikace.

- Při více kamerách zakázat paralelní motorické testy; odlišit správný DID, channel a počet čoček.

- Příkaz 0x112 a odpověď 0x113 nemusí znamenat pohyb. Ret/status a skutečný pohyb se musí zaznamenat zvlášť.


---

# 8. Vyhodnocovací matice: chyby a další postup

| Pozorování | Pravděpodobná příčina / neplatný závěr | Doporučený zásah |
| --- | --- | --- |

| Account restore timeout / HTTP | Blokováno ještě před kamerou; nelze hodnotit PTZ. | Pokus z běžného Windows hostu, jeden retry; rozlišit DNS/TLS/HTTP. |

| Inventář C201 nenalezen | Region, účet, device list, rate limit; nemusí být chyba LAN. | Zkontrolovat Xiaomi Home/region a rozdíl mezi profilem a živým seznamem. |

| CS2 LAN search bez odpovědi | Firewall UDP, AP isolation, směrování; nelze usuzovat na RTSP. | Stejný subnet; povolení UDP pro proces; PCAP discovery. |

| CS2 handshake ok, bez media | Špatný quality/channel, start streamu, jiný vendor. | Auto/0 a připojit jeden profil; zkontrolovat first_media. |

| RTSP 503 hned po otevření | Gateway ještě není ready, v našem logu potvrzeno. | Čekat na rtsp_description ready a opakovat DESCRIBE. |

| HEVC missing-reference | Potenciálně připojení mimo keyframe; příčina neprokázána. | Nechat dekodér běžet, sledovat keyframe a reálné poškození. |

| 0x112 write ok, bez 0x113 | Jen zápis, bez potvrzení. | Zkontrolovat command dispatcher, rx channel, timeout. |

| 0x113 přijata, motor stojí | ACK může být status; jiný payload; krajní poloha; auto-tracking. | Číst ret/pozici; fyzická kontrola; neměnit náhodné command ID. |

| Obraz se mění, ale objektiv ne | Expozice, dynamická scéna, HEVC frame mismatch. | Robustní odhad posunu, externí kamera na motor, vyšší rozlišení. |

| Změna MIoT přes cloud, bez LAN | Specifikace dovoluje R/W, ale nezaručuje lokální transport. | Test odpojení WAN pouze po potvrzení stabilní funkce. |

| PTZ druhé čočky nefunguje | Čočka může být pevná; u CW500 je druhá čočka fixní. | Nesnažit se hýbat statickým objektivem; správný channel. |



# 9. Tabulka pro ověřování více kamer

| Kamera / model ID | FW / region | Transport / video | PTZ / audio / offline |
| --- | --- | --- | --- |

| C201 / xiaomi.camera.c201 | 5.3.2_0016 / de | CS2+TCP, H265 848×480, L2 OK | PTZ nepotvrzeno; audio ne; offline ne |

| C400 / doplnit | __________ | __________ | __________ |

| Mi 360° / doplnit | __________ | __________ | __________ |

| Outdoor / doplnit | __________ | __________ | __________ |

| Další / doplnit | __________ | __________ | __________ |



Pořadí pro každou další kameru: (1) inventář/model ID; (2) read-only diagnostika; (3) stream; (4) příkaz/reply; (5) fyzický pohyb; (6) MIoT R/W; (7) audio; (8) WAN. Každý řádek doplnit důkazem a datem.

# 10. Předávací zadání pro vývojového agenta

- Neimplementovat Home Assistant integraci, dokud není na aspoň jedné fyzické kameře odhalen a prokázán mechanismus PTZ.

- Nejdřív přečíst existující MotorStep v internal/xiaomi/protocol/miss/client.go a session dispatch ve stream/stream.go; nepsat druhého klienta.

- Vytvořit read-only audit příkazů a diagnostický režim PTZ „single-step guarded“ s jedinou session a podmínkou funkčního videa.

- Striktní rozlišení error stage: cloud_auth / inventory / negotiation / cs2 / media / rtsp / ptz_tx / ptz_rx / motion.

- Zaznamenat anonymizované command ID, payload, odpověď, latenci a fyzický důkaz. Nikdy logovat token, DID, klíče a dočasné RTSP URL.

- Po prvním potvrzeném směru otestovat zbývající směry a vyrobit per-model capability mapu; až následně navrhnout ploché API.


---

# 11. Odkazy a původ tvrzení

A. MÍSTNÍ ZÁZNAMY (ověření na naší C201, bez nového měření)

- ZAZNAM_VYSLEDKU_C201(1).md (7. 11. 2026), sekce A–U, zejména M–U: CS2/MISS, DeviceInfo, pokusy PTZ a timeouty.

- PLAN_C201(1).md a START_HERE.txt: požadovaná úroveň L2, diagnostický postup a omezení.

- C201_PTZ_navazujici_plan.md: navržený, dosud nevykonaný přísný PTZ test.

B. VEŘEJNÉ ZDROJE (stav při kontrole 7. 11. 2026)

- Xiaomi Camera Viewer README – modely, CW300/CW400/CW500/CW700S, motor 0x112, diagnostics: https://github.com/lubos-hnanicek/xiaomi-camera-viewer

- Xiaomi Camera Viewer – diagnostický postup: https://github.com/lubos-hnanicek/xiaomi-camera-viewer/blob/main/docs/camera-diagnostics.md

- go2rtc Xiaomi Mi Home – transport, cloudové klíče, profily a kanály: https://go2rtc.org/internal/xiaomi/

- go2rtc #1982 – přehled hlášené kompatibility: https://github.com/AlexxIT/go2rtc/issues/1982

- go2rtc #2162 – Xiaomi PTZ, legacy a motor status: https://github.com/AlexxIT/go2rtc/issues/2162

- go2rtc #2531 – návrh ONVIF PTZ přes MISS 0x112: https://github.com/AlexxIT/go2rtc/pull/2531

- go2rtc #2290 – nevyřešená podpora C201 v upstreamu: https://github.com/AlexxIT/go2rtc/issues/2290

- C201 MIoT specifikace – vlastnosti a akce: https://home.miot-spec.com/spec/xiaomi.camera.c201

- Xiaomi C400 oficiální FAQ – PTZ v aplikaci: https://www.mi.com/cz/support/faq/details/KA-43338/

- Oficiální Xiaomi Home HA – video není podporováno: https://github.com/XiaoMi/ha_xiaomi_home/discussions/78

# 12. Doporučený další experiment

Než začneme další kameru, dokončit C201: z běžného Windows hostu navázat jednu CS2/TCP session; získat 8–10 výchozích snímků 848×480; ve stávajícím zdrojovém kódu potvrdit přesný formát MotorStep; odeslat maximálně jeden bezpečný krok; zaznamenat 0x113 a fyzický pohyb. Teprve pak opakovat s přesně identifikovanou C400, Mi 360° a Outdoor kamerou.

Bez zásahu do firmwaru; bez rootu, resetu, kalibrace, formátování SD či hádání nezdokumentovaných motorických příkazů. Dokument je pracovní plán, nikoli potvrzení univerzální kompatibility.
