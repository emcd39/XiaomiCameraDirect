# Architektura a bezpečnostní hranice

```text
Windows prohlížeč
       |
       |  HTTP 127.0.0.1:18880 (bez gateway tokenu v klientovi)
       v
Diagnostic Console (Go, statické UI)
       |   |               |                    |
       |   |               |                    +-- PTZ: jediný externí managed-ptz-http-probe.exe
       |   |               |                         (runtime verifier + 8 stabilních snímků + jeden LEFT)
       |   |               +-- FFmpeg record worker ----> MKV segmenty na disk/NAS
       |   +-- jeden FFmpeg preview worker ----> sdílený MJPEG náhled
       +-- read-only GET /healthz a /v2/info
       +-- explicitní GET /v2/cameras [může spustit camera session]
                    |
                    v
            Xiaomi Local Gateway (managed API 127.0.0.1 / RTSP)
                    |
               existující CS2/TCP session
                    v
             Xiaomi C201 (IP nezávisle neověřena)
```

## Záměrně oddělené části

- **Video**: RTSP čte gateway, ne C201 přímo. FFmpeg transkóduje jen webový náhled na MJPEG, protože H.265 není spolehlivě podporován prohlížeči. Jeden FFmpeg dekodér obsluhuje libovolný počet lokálních návštěvníků.
- **Nahrávání**: druhý (výslovně spuštěný) FFmpeg worker kopíruje HEVC video do MKV segmentů, nedělá nový CS2 handshake. Zastavený záznam se ukončuje bez automatického reconnectu. Na pomalé síti je potřeba ověřit chování při výpadku.
- **Motor**: konzole nemapuje vlastní CS2 příkazy. Spouští samostatný již existující ověřovací helper s přesnými parametry z E1 a s `PTZ_WRITE_ENABLED=true` pouze během jednoho explicitního testu. Pomocný program není součástí ZIPu, protože jeho zdroj a binárka jsou v soukromém Windows projektu.
- **SD karta / další funkce**: pouze capability explorer. Bez podložené znalosti MIoT/CS2 se nevytváří fiktivní funkcionalita. NAS záznam je dostupná samostatná funkce, nikoli náhrada SD API.

## Autorizace a ochrany

- Na backendu se používaný Bearer token čte z `token_file`. `/api/status` nikdy nevrací raw `/v2/info` ani `/v2/cameras` a předává jen odvozené a sanitizované indikátory. Žádný Xiaomi DID či RTSP URL v JSON prohlížeče.
- Nastavení nepovoluje TCP bind na LAN ani gateway API mimo loopback. HTTP wrapper validuje `Host`, `Origin`, `X-XLG-Action`, má CSP a vypnuté cachování.
- PTZ dovoluje maximálně jednu současnou operaci; žádný retry. Backend vyžaduje explicitní `ONE-LEFT-APPROVED` a checkbox fyzických podmínek. Stav mocku je vždy `simulated`, nikoli skutečný pohyb.
- Žádné `GET /v2/cameras` automaticky během status pollingu: správná historická implementace `Streams()` v provideru může navázat session. Inventory je jen na explicitní tlačítko.
- Program umožňuje RTSP URL pouze pro loopback nebo adresy z `allowed_rtsp_hosts`; účet/relaci v URL s vloženým uživatelským jménem/heslem odmítá. Tím se omezuje omyl připojení přímo ke kameře.
- Systém není určen k vystavení přes reverse proxy. Pro vzdálené použití nejprve doplnit autentizaci, TLS a přístupová oprávnění.

## Známá omezení

1. RTSP náhled a nahrávání vyžadují URL získanou z aktuálně vybrané připravené C201 v managed inventáři. Starší nastavení `rtsp_url_file` se kvůli bezpečnému párování kamery a streamu ignoruje; pokud inventář URL neposkytne, náhled zůstane nedostupný. Obnova inventáře nebo změna výběru nejprve ukončí náhled; při timeoutu zůstane původní výběr beze změny.
2. `GET /v2/info` 200 = API je dostupné; není to důkaz streamu ani CS2 session. Status označuje relaci jako neověřenou.
3. `write_status` v odpovědi helperu je pouze best-effort dekódován ze sanitizovaných řádků. Pokud helper vrací jiný formát, UI ponechá `unverified`. Nepředpokládat potvrzený motorický pohyb.
4. MJPEG náhled není vhodný pro více vzdálených uživatelů; pro HA/Frigate lepší RTSP/WebRTC bez opakovaného transkódování.
5. SD karta, motion events, audio a IR nemají implementaci, dokud není lokálně doložený protokol.
6. Nahrávání neobsahuje automatickou retenci/disk quota; na NAS je vhodné nastavit samostatné limity nebo správu diskového prostoru.
7. Předávání RTSP capability URL do argumentů FFmpeg znamená viditelnost pro správce Windows procesu. Soubor s URL a nahrávky chránit ACL.
8. Na Linuxu byl backend offline testován standardní Go sadou i `-race`. Windows binárka byla cross-compiled, **nikoli spuštěna na Windows / skutečné C201**.

## Rozšíření

Doporučený další směr je přesvědčivý **read-only capability discovery** v samotné gateway: oddělený `/v2/cameras/{id}/capabilities` s jasným rozlišením `implemented`, `unsupported`, `unverified`. Teprve po ověření lokálních MIoT/CS2 příkazů přidávat SD status a list/read, potom IR/motion, pak ostatní směry PTZ. UI má již pro tyto funkce prostor.
