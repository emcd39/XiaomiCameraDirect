# Funkce kamer Xiaomi v přímé integraci Home Assistantu

V tabulce jsou oddělené schopnosti popsané výrobcem, funkce v přímé integraci a výsledky z konkrétních zkoušek. Podrobnosti a logy jsou v [záznamu přejímek](HA-DIRECT-PILOT.md).

| Kamera a model účtu | Vybrané schopnosti podle výrobce | Fyzicky ověřeno v přímém HA | Hotovo v kódu, čeká na kameru | Chybí v přímém HA |
| --- | --- | --- | --- | --- |
| C701 · `chuangmi.camera.079ae2` | 4K, otáčení, obousměrný zvuk, noční vidění, detekce osob a koček nebo psů, tracking, clona, microSD. [Xiaomi C701](https://www.mi.com/global/product/xiaomi-smart-camera-c701/) | Živý obraz 3840 × 2160, směrový pohyb, JPEG, MP4. Přítomnost zvukové stopy a odtlumení jsou potvrzené. | Slyšitelný poslech, hovor do kamery, obnova po výpadku. | Živé události osoby a zvířete, nastavení detekce, clona, tracking, čtení SD a přehrávání záznamů z karty. |
| C201 · `xiaomi.camera.c201` | Otáčení, hovor, pohybové události, osoby, zvířata, SD a noční režim. [Xiaomi C201 FAQ](https://www.mi.com/global/support/faq/details/KA-600067/) | Historicky ověřený živý obraz, směrový pohyb, JPEG a MP4. Zvuk byl dekódovaný na testovací stanici. Dne 23. 11. 2026 prošel JPEG 848 × 480, ale HLS nedodal snímky. [Opakovaná přejímka](#opakovaná-přejímka-23-11-2026). | Poslech na kartě HA. Diagnostické entity prošly v simulaci HA. Dne 22. 11. prošlo čtení skutečných hodnot nočního režimu, kontrolky, nastavení detekce a SD přes Xiaomi účet z pracovní kopie. | Hovor, živé události, změna nastavení, tracking, režim soukromí a SD klipy. |
| C400 · `chuangmi.camera.039a04` | 2,5K, otáčení, noční vidění, obousměrný zvuk, detekce a sledování osob, microSD. [Xiaomi C400](https://www.mi.com/global/product/xiaomi-smart-camera-c400/) | Obraz 864 × 480 a 2560 × 1440, směrový pohyb, dekódovaný JPEG a MP4. U příjmu zvuku potvrzen Opus z kamery a AAC ve streamu HA při 2560 × 1440. | Slyšitelný poslech, zvuk u nižšího rozlišení, delší běh a obnova relace. | Hovor do kamery, živé události osob, nastavení, tracking a SD klipy. |
| Mi 360° 1080p · `chuangmi.camera.026c02` | Xiaomi u přesného ID uvádí variantu SE+. U řady Mi 360° 1080p popisuje obousměrný zvuk, otáčení a microSD. [Katalog modelů Xiaomi](https://github.com/XiaoMi/xiaomi-miloco/blob/main/backend/miot/src/miot/configs/camera_extra_info.yaml) · [FAQ produktové řady](https://www.mi.com/global/support/faq/details/KA-121945/) | Dříve potvrzený živý obraz. Jeden krok doleva je doložen pouze přes gateway. | Přímý směrový pohyb, JPEG a krátký záznam. | Modelové nastavení, živé události, audio a SD klipy. |
| Outdoor AW300 · `mxiang.camera.mod13` | Pevný záběr, obousměrný zvuk, detekce osob, zóny, reflektory, siréna, noční vidění a microSD. [Xiaomi AW300](https://www.mi.com/global/product/xiaomi-outdoor-camera-aw300/) | 22. 11. 2026 přímý živý obraz 2304 × 1296, dekódovaný JPEG a MP4 z akcí HA. Přímá sonda zachytila Opus společně s obrazem přes UDP. | Slyšitelný poslech, delší běh a obnova relace. | Hovor do kamery, živé události osob, nastavení a SD klipy. Mechanické PTZ se na pevné kameře netestuje. |

## Opakovaná přejímka 23. 11. 2026

Testovací Windows host, Home Assistant `2026.10.4`, Xiaomi Camera Direct `0.3.5`, region `de`. Použity byly přímé entity. Verze firmwaru těchto zařízení v registru HA nebyly uvedeny.

| Kamera | Živé HLS přes HA | Snímek přes HA | Krátký záznam přes HA |
| --- | --- | --- | --- |
| C201 | Timeout, 0 snímků | Platný JPEG, 848 × 480 | Přeskočeno po timeoutu HLS |
| C400 | 40 dekódovaných snímků, 2560 × 1440 | Platný JPEG | Platný MP4, přibližně 21,7 s |
| Outdoor AW300 | 21 dekódovaných snímků, 2304 × 1296 | Platný JPEG | Platný MP4, přibližně 22,5 s |
| Mi 360 SE+ | Timeout, 0 snímků | HTTP 500 | Přeskočeno po timeoutu HLS |

Mediální sonda dekódovala snímky a začátek záznamů pomocí FFmpegu. Návratový kód sondy popisuje dokončení běhu, výsledky jednotlivých funkcí jsou v řádcích logu. [Výsledky médií](../.test-env/ha-native-win-py314/direct-camera-matrix-20261123-retry.log).

- **Dostupnost:** na hostiteli HA odpověděly na cílenou sondu CS2 C201, C400, AW300 i referenční C701. Mi 360 SE+ neodpověděla, přestože vybraná zdrojová adresa patřila do stejné podsítě. Sonda spuštěná předtím v omezeném prostředí vrátila pět timeoutů. Pro výběr kamer použij výsledek sondy z hostitele HA. Odpověď C201 potvrzuje průchod sondy, úspěšný HLS z ní neplyne. [Sonda z hostitele](../.test-env/ha-native-win-py314/camera-reachability-20261123-host.json).
- **Příjem zvuku:** FFmpeg z HA dekódoval 2 s zvuku C400. HLS z AW300 ani s povoleným příjmem nenabídl zvukovou stopu. Dříve zachycený Opus z AW300 je důkaz zvuku na vstupu integrace. C201 a Mi 360 se v tomto běhu kvůli chybě živého přenosu zvukově netestovaly. [Dekódování zvuku](../.test-env/ha-native-win-py314/direct-camera-audio-20261123-acceptance.log).
- **PTZ:** API má po čtyřech směrových tlačítkách pro C201, C400 a Mi 360. Při odečtu měla C400 všechna čtyři dostupná, C201 a Mi 360 čekaly na aktivní mediální relaci. Pohyb se znovu nespouštěl. V tabulce platí dřívější fyzické ověření C201 a C400 a historický krok Mi 360 přes gateway. AW300 je pevná a PTZ tlačítka nemá. [Odečet HA](../.test-env/ha-native-win-py314/direct-ha-states-20261123-acceptance.log).
- **Diagnostika C201:** pokus o čtení nočního režimu, kontrolky, detekce a SD přes Xiaomi účet tentokrát vrátil `AccountError`. Dřívější úspěšný odečet zůstává doložený. Diagnostické entity v běžícím Windows HA při kontrole nebyly nasazené. [Pokus o čtení](../.test-env/ha-native-win-py314/c201-cloud-diagnostics-20261123-acceptance.log) · [Předchozí výsledek](HA-DIRECT-PILOT.md#přesun-do-sítě-19216820-24--22-11-2026).
- **Izolovaná simulace HA:** pět ze šesti testů proběhlo bez selhání, včetně čtení diagnostických entit C201, obrazu a sdílené obnovy relace. Jediná chyba byla v testu obnovy hovoru C701 (`TalkbackError` při zahájení simulovaného hovoru). Celá sada skončila kódem 1. [Výstup testu](../.test-env/ha-native-win-py314/direct-ha-e2e-20261123-acceptance.log).

## Fyzické zakrytí objektivu

| Model | Co uvádí Xiaomi | Stav v Xiaomi Camera Direct |
| --- | --- | --- |
| C201 | Objektiv se při aktivaci fyzicky zasune do těla. [Xiaomi C201](https://www.mi.com/uk/support/faq/details/KA-600067/) | Přidáno volitelné čtení **Nastavení zapnutí kamery** přes Xiaomi účet. Poloha objektivu, vazba na režim soukromí a ovládání z HA čekají na fyzické ověření. |
| C701 | Fyzické zakrytí lze aktivovat z živého obrazu v Xiaomi Home. [Xiaomi C701](https://www.mi.com/global/support/faq/details/KA-608921/) | Ovládání a čtení režimu clony čekají na ověření. |
| C400 | Xiaomi popisuje režim spánku. [Xiaomi C400](https://www.mi.com/global/support/faq/details/KA-12475/) | Fyzická clona není doložená. |
| Mi 360 SE+ a AW300 | Fyzická clona není potvrzená pro zdejší modely. | Funkce v HA zatím není dostupná. |

Diagnostická entita C201 je standardně vypnutá. Pokud je povolená, čte se v témže dotazu jako noční režim, kontrolka a SD. Chybějící nebo neplatná hodnota znamená `unavailable`. Zkouška F09 má porovnat hodnotu při záměrném zakrytí a odkrytí v Xiaomi Home se skutečnou polohou objektivu a dostupností obrazu, zvuku a záznamu.

Po přidání diagnostiky prošlo 10 testů Xiaomi účtu a 6 testů izolovaného HA. Test obnovy hovoru C701 teď používá stejnou čtecí metodu jako audio relace a ověřuje příjem snímku před zahájením hovoru i po obnovení spojení. Jde o simulaci. [Regresní log](../.test-env/ha-native-win-py314/f09-read-only-regression.log).

## Jak přidávat další kamery a funkce

- Kameru rozpoznanou v účtu zařadíme do inventáře i bez profilu přenosu. Po obnově inventáře se nová kamera zařadí automaticky. Výslovná vyřazení uživatele se zachovají. Chybějící profil se v HA zobrazí jako `model_profile_pending`.
- Každou funkci uváděnou pro konkrétní model ve specifikaci zařadíme do plánu. Zaznamenáme model z účtu, dostupnou revizi firmwaru, region, zdroj specifikace a požadovanou operaci. Funkce, ke které chybí podklad, zůstane k ověření.
- Jakmile je známá cesta v protokolu a máme implementaci, funkci nabídneme uživateli. Samotná specifikace výrobce nestačí k odeslání příkazu. Povolení funkce v integraci je samostatné od nastavení kamery, mikrofonu a režimu soukromí.
- K fyzicky ověřené funkci uložíme datum, firmware, region, použitý backend a verzi HA, výsledek a odkaz na log. Jednorázový síťový výpadek zaznamenáme jako překážku zkoušky. U detekce evidujeme zvlášť čtení nastavení a skutečnou událost v HA.

Izolovaný test HA potvrdil, že se nový model po obnově objeví s důvodem `model_profile_pending`, dříve vyřazená kamera se znovu nezařadí a původní kamera po návratu trasy zachová svoji entitu. [Výsledek testu](../.test-env/ha-native-win-py314/camera-catalog-ha-recheck.log).
