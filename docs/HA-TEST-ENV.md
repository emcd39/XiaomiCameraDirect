# Lokální testovací Home Assistant

Provozní poznámky k nativní Windows instanci používané pro vývoj Xiaomi Camera Direct. Neobsahují přihlašovací údaje.

## Instance a spuštění

- Kořen prostředí: `.test-env/ha-native-win-py314/`
- Konfigurace HA: `.test-env/ha-native-win-py314/config/`
- Python prostředí: `.test-env/ha-native-win-py314/venv/`
- Spouštěcí skript: `.test-env/ha-native-win-py314/start-native-ha.ps1`
- Skript odmítne start, pokud už port `56264` odpovídá. HA spustí skrytě a zapisuje výstup do `native-stdout.log` a `native-stderr.log`; PID uloží do `native.pid`.
- Úspěšná startovací kontrola očekává UI `200`, API `401` při neautentizovaném probe a JavaScriptové prostředky `200`. API `401` v tomto probe znamená, že server odpověděl a vyžaduje přihlášení.
- Před opětovným startem ověř, zda instance už běží. Zámek HA nemaž, dokud není ověřeno, že proces vlastnící konfiguraci skončil.

## Panel a přímá karta

- V testovací instanci otevři panel **Xiaomi přímo → Kamery**: `http://127.0.0.1:56264/xiaomi-direct/kamery`.
- `127.0.0.1` funguje jen na stejném počítači jako testovací HA. Port `56259` patří jiné/proxy instanci; cesta Xiaomi přímo tam není zaregistrovaná.
- C701 entita: `camera.xiaomi_c701_direct`.
- Resource v Lovelace: `/xiaomi-direct/xiaomi-direct-camera.js?v=0.3.5.1`. Parametr URL označuje revizi karty pro cache prohlížeče.
- Zdroj karty: `custom_components/xiaomi_direct_pilot/www/xiaomi-direct-camera.js`. Kopie nasazená v testovacím HA je pod `config/custom_components/xiaomi_direct_pilot/www/`.
- SHA-256 nasazené revize karty: `7377F08F6B9288EFFEB1A56691736BEF0851A7FF841A2584E86FA67FE778A06C`.
- Karty používají `custom:webrtc-camera`; resource přehrávače je `/webrtc/webrtc-camera.js?v=v3.6.1`.

## Ovládání zvuku C701

V kartě **Xiaomi C701 Direct** zvol **Se zvukem**, klikni na ikonu reproduktoru vpravo dole a nastav **Hlasitost poslechu** pod obrazem. Tlačítko **Podrž pro mluvení** je pod obrazem. V Codexu byla ověřena ikona i posuvník. Naměřenou úroveň signálu a otevřené fyzické zkoušky uvádí [záznam přejímky](HA-DIRECT-PILOT.md).

## Použití Codexu

Panel lze kontrolovat v in-app prohlížeči Codexu přes jeho otevřenou kartu Home Assistantu. Pro tuto kontrolu není potřeba spouštět Průzkumníka souborů ani otevírat systémový browser. Pokud stránka neodpovídá, nejprve ověř dostupnost portu `56264` a stav procesu podle výše uvedeného skriptu; samotná uložená Lovelace konfigurace nedokládá, že HA právě běží.
