# Lokální testovací Home Assistant

Provozní poznámky k nativní Windows instanci používané pro vývoj Xiaomi Camera Direct. Neobsahují přihlašovací údaje.

Přímé kamery otevři v [Xiaomi přímo → Kamery](http://127.0.0.1:56264/xiaomi-direct/kamery). V Codexu použij záložku s portem **56264**. Přehled na portu **56259** je jiná instance HA.

## Instance a spuštění

- Kořen prostředí: `.test-env/ha-native-win-py314/`
- Konfigurace HA: `.test-env/ha-native-win-py314/config/`
- Python prostředí: `.test-env/ha-native-win-py314/venv/`
- Spouštěcí příkaz z kořene projektu: `./scripts/start-native-direct-ha.ps1`. Nejprve ověří `http://127.0.0.1:56266/api/streams` a podle potřeby spustí místní go2rtc. Pak použije stávající HA, nebo ho spustí. Pro přímý panel není nutná Xiaomi Gateway.
- Nativní spouštěč v `.test-env/ha-native-win-py314/start-native-ha.ps1` volá tutéž kontrolu go2rtc i při samostatném startu. HA běží skrytě a zapisuje výstup do `native-stdout.log` a `native-stderr.log`. PID zapisuje do `native.pid`. go2rtc zapisuje do `media-tools/go2rtc-direct.log` a `media-tools/go2rtc-direct.err`.
- Úspěšná startovací kontrola očekává UI `200`, API `401` při neautentizovaném probe a JavaScriptové prostředky `200`. API `401` v tomto probe znamená, že server odpověděl a vyžaduje přihlášení.
- Před opětovným startem ověř, zda instance už běží. Zámek HA nemaž, dokud není ověřeno, že proces vlastnící konfiguraci skončil.

## Panel a přímá karta

- V testovací instanci otevři panel **Xiaomi přímo → Kamery**: `http://127.0.0.1:56264/xiaomi-direct/kamery`.
- `127.0.0.1` funguje jen na stejném počítači jako testovací HA. Port `56259` patří jiné/proxy instanci; cesta Xiaomi přímo tam není zaregistrovaná.
- C701 entita: `camera.xiaomi_c701_direct`.
- Resource v Lovelace: `/xiaomi-direct/xiaomi-direct-camera.js?v=00d4394e98c0a32b`. Integrace při spuštění odvodí parametr `v` z prvních 16 znaků SHA-256 souboru karty. Při změně obsahu prohlížeč dostane novou URL automaticky.
- Zdroj karty: `custom_components/xiaomi_direct_pilot/www/xiaomi-direct-camera.js`. Kopie nasazená v testovacím HA je pod `config/custom_components/xiaomi_direct_pilot/www/`.
- SHA-256 nasazené revize karty: `00D4394E98C0A32BAC2C03587DFE5813AE324CD2BC4873BF51EE7275B52A250E`.
- Karty používají `custom:webrtc-camera`; resource přehrávače je `/webrtc/webrtc-camera.js?v=v3.6.1`.
- V přímém panelu karty míří na entity `camera.*_direct`. WebRTC integrace v HA s nimi komunikuje přes lokální přehrávač go2rtc na portu `56266`. Opakované odmítnutí spojení na tento port znamená, že nedostupný je přehrávač na hostiteli HA. Pro nedostupnou kameru s běžícím go2rtc ověř adresu a trasu preflightem.
- Lokální kopie opraveného přímého panelu, registrace zdrojů, souboru karty a spouštěče je v `.test-env/ha-native-win-py314/checkpoints/2026-11-22-direct-panel/`. Kopie neobsahuje přihlašovací soubory. Aktuální spouštěč je v `scripts/`, takže změna zůstane dohledatelná v projektu.

### Obnova panelu ze souboru v Gitu

1. Spusť testovací HA výše uvedeným skriptem. Ověř registraci zdrojů karet `/xiaomi-direct/xiaomi-direct-camera.js` a `/webrtc/webrtc-camera.js` v Lovelace. Použij aktuální parametr verze registrovaný integrací.
2. V HA otevři panel **Xiaomi přímo** s cestou `xiaomi-direct` nebo ho vytvoř jako samostatný panel. V režimu úprav otevři editor surové konfigurace a vlož obsah [exportu Lovelace](examples/lovelace-xiaomi-direct.json). Export zachovává záložku **Kamery** (`kamery`), pořadí pěti karet a PTZ akce otočných kamer. Pokud se ID entit v obnovené instanci liší, uprav cíle karet i PTZ tlačítek podle registru HA.
3. V nastavení integrace **Xiaomi Camera Direct** zapni experimentální zvuk pro C201, C400, C701 a AW300, pokud obnovuješ zdejší testovací konfiguraci. Obnova Lovelace tyto volby integrace nenastaví. Mi 360 SE+ má v uloženém panelu obrazový profil.
4. Po otevření záložky zkontroluj pět karet a PTZ tlačítka. Na dostupné kameře se zvukem po načtení zvukové stopy ověř ikonu reproduktoru vpravo dole a posuvník hlasitosti pod obrazem. Tlačítko hovoru má místo pod obrazem C701. Výsledky fyzických zkoušek jsou v [matici schopností](HA-CAMERA-CAPABILITIES.md).

Export obsahuje pouze konfiguraci panelu. Uložení přihlašovacích údajů a místních registrů řeší samostatný zálohovací bod v `.test-env/`.

## Fyzické zkoušky C701

Pro ověření dostupnosti jen C701 použij na hostiteli testovacího HA tuto sondu. Ostatní modely jsou odfiltrované před odesláním paketů.

```powershell
& .\.test-env\ha-native-win-py314\venv\Scripts\python.exe -B scripts/ha_camera_reachability.py --model chuangmi.camera.079ae2
```

Při timeoutu unicast sondy ověř běžnou streamovací cestu C701 v HA. Navázání skutečné relace může použít i broadcast.

Pokud se po přesunu mezi sítěmi liší adresa v účtu Xiaomi od uložené adresy HA, znovu načti položku **Xiaomi Camera Direct** v nastavení integrací. Tím se obnoví inventář a trasy při zachování entit. Vyčkej na zapsání inventáře a ověř konkrétní stream. U C701 tento postup 22. 11. obnovil přenos po změně adresy na `192.168.2.188`. [Záznam zkoušky](HA-DIRECT-PILOT.md).

## Ovládání zvuku podporovaných kamer

Ve zdejším účtu v regionu `de` mají **C201, C701, C400 a AW300** povolený příjem zvuku v nastavení Xiaomi Camera Direct. Každá karta používá jeden profil `mode: mse`, `media: video,audio`, `muted: true`, `ui: true`, bez seznamu `streams`. Po načtení zvukové stopy je vpravo dole ikona reproduktoru. Jedním kliknutím zapneš nebo ztlumíš poslech. Pod obrazem je **Hlasitost poslechu**. C701 navíc nabízí **Podrž pro mluvení**. U C201, C400 a AW300 je `push_to_talk: false`. AW300 při zapnutém zvuku používá ověřenou cestu UDP. Přímý audio profil Mi 360 SE+ čeká na ověření.

Uložený panel s tímto profilem na čtyřech kamerách, jeho registrace zdroje a karta JS jsou v `.test-env/ha-native-win-py314/checkpoints/2026-11-22-supported-audio-aw300/`.

## Použití Codexu

Panel lze kontrolovat v in-app prohlížeči Codexu přes jeho otevřenou kartu Home Assistantu. Pro tuto kontrolu není potřeba spouštět Průzkumníka souborů ani otevírat systémový browser. Pokud stránka neodpovídá, nejprve ověř dostupnost portu `56264` a stav procesu podle výše uvedeného skriptu; samotná uložená Lovelace konfigurace nedokládá, že HA právě běží.
