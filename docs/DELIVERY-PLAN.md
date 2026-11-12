# Plán dodání Xiaomi Local Gateway pro C201

Stav k 6. 11. 2026. Referenční kamera je Xiaomi Smart Camera C201, model `xiaomi.camera.c201`, s firmwarem hlášeným jako `5.3.2_0016` a účtem v regionu EU.

## Co má uživatel dostat

V Home Assistantu přidá integraci **Xiaomi Local Gateway**, přihlásí se k Xiaomi, vybere C201 a hned uvidí náhled. Z kamery v HA pustí živý obraz, pořídí snímek a posune kameru nahoru, dolů, doleva nebo doprava. Ovládání lze použít i v automatizacích. Po výpadku nebo restartu se kamera sama vrátí a zachová si stejné entity a nastavení.

Pro HA OS se služba dodá jako náš doplněk, pro HA Container jako jeden připravený kontejner. Správa účtu, výběr kamer a běžné ovládání probíhají v jednom průvodci HA. Při běžné instalaci uživatel nepotřebuje vlastní YAML, ruční API token ani samostatně spravovaný bridge. [Dokumentace doplňků Home Assistant](https://developers.home-assistant.io/docs/apps/)

## Výchozí stav

- Dočasný pilot ověřil přihlášení k Xiaomi EU a načtení přesného modelu C201. Jeho samostatná integrace vytvořila entity v testovacím HA.
- Oficiální cesta skončila časovým limitem při navazování relace. Kompatibilní cesta má nakonfigurovaný Xiaomi zdroj, ale místní RTSP vrací `OPTIONS 200` a `DESCRIBE 404` bez SDP. Snímek vrací HTTP 503 a PyAV hlásí `InvalidDataError` při TCP i UDP. Zastavení je lokalizované mezi konfiguraci zdroje a vytvoření použitelného RTSP popisu, přesnou chybu protokolu zatím neznáme. Žádná cesta nedodala dekódovaný snímek.
- Headless bridge recheck potvrdil propojený účet, C201 `online=true` ale `lan_online=false`, aktivní konfiguraci `compat` a žádné naměřené FPS. Jeden čerstvý snapshot (`max_age=0`) skončil `503 RTSP_SOURCE_FAILURE`; dekódovaný JPEG nevznikl. WSL2 Ubuntu je v oddělené virtuální podsíti, takže plánované Linuxové srovnání ve stejné LAN nešlo provést. Přesné síťové prostředí potřebné k pokračování je uvedeno v [NEXT-TASK.md](NEXT-TASK.md).
- Izolovaný běh aktuálního stromu `.test-env/evidence/test-local-20261106-121819-f540d7c5.log` prošel s `RESULT=PASS`: Go race/vet včetně persistovaného výběru, managed `/v2/info` a kompatibility legacy JSON dekodéru, sedm klientských testů, HA konfigurace a průvodce, entity/capabilities, čtyři směry mock PTZ, výpadek a obnova. Běžel proti mockům a statickému poskytovateli, nikoli proti C201. Fyzická C201 čeká na ověření obrazu a ovládání. [Záznam implementace](IMPLEMENTATION-STATUS.md)

## Funkce a jejich přejímka

| Rozsah | Funkce | Co musí projít na skutečné C201 |
| --- | --- | --- |
| Povinné pro vydání | Účet a výběr zařízení | Čistá instalace, přihlášení k EU účtu, výběr C201 a vytvoření jedné sady entit. Po restartu a opětovném přihlášení zůstanou stejné identifikátory. |
| Povinné pro vydání | Živý obraz a snímek | HA zobrazí aktuální snímek a v prohlížeči přehraje alespoň 10 minut měnícího se obrazu. Ověřit start, dobu do prvního snímku, kodek a obnovu po výpadku. |
| Povinné pro vydání | Posun kamery | Z HA ověřit čtyři směry na fyzickém záběru a stejnou akci z automatizace. U souvislého pohybu ověřit zastavení, u krokového bezpečný konec kroku. Chybný nebo opožděný příkaz se nesmí vykonat po obnově spojení. |
| Povinné pro vydání | Stav a obnova | Nedostupnost kamery a vypršení přihlášení mají odlišný stav a čitelnou zprávu. Po obnovení sítě se vrátí čerstvý obraz bez zásahu a reautorizace zachová entity. |
| Další funkce k dodání | Zvuk z kamery, kvalita obrazu, noční režim | Read-only HA senzory nyní zahrnují C201 MIoT `Night Shot` i stavové přepínače kontrolky, detekce pohybu, osob a mazlíčků. Jejich aktuální čtení a stav entit na HA čekají na autorizovaný běh. Detekční entity ukazují zapnutí funkce, nikoli událost detekce. Fyzicky porovnat obraz ve tmě a přepínání režimu až po samostatném ověření. Zvuk a volbu kvality vést zvlášť. |
| Další funkce k dodání | Soukromí | Ověřit fyzické zakrytí čočky a stav v HA. Při zakrytí přestane HA podávat živý obraz a starý snímek se nevydává za aktuální. Po otevření se živý obraz vrátí. |
| Další funkce k dodání | Události a nahrávání | Vyvolat pohyb, ověřit událost a spuštění HA automatizace. Ověřit uložení a přehrání záznamu z HA nebo podporovaného NVR. Uvést zdroj detekce a zda vyžaduje cloud či NVR. |
| Samostatně ověřit | Mluvení do kamery, polohy, automatické sledování, přehrávání z microSD nebo Xiaomi cloudu | Každou funkci posoudit zvlášť. U mluvení ověřit zvuk z reproduktoru kamery, u poloh a sledování pohyb a návrat, u historie přehrání skutečného klipu. |

Seznam dalších funkcí ověříme na konkrétním firmwaru v Xiaomi Home a v dostupném protokolu. Každá dostane stav `ověřeno v HA`, `blokováno` nebo `mimo dohodnutý rozsah` s důvodem. Integrace zobrazí pouze ověřené ovládací prvky. Pokud se některá požadovaná funkce ukáže jako nedostupná, zůstane otevřená do dohody o rozsahu. Podpora čtyř směrů neznamená podporu optického zoomu. [Funkce a ovládání C201 podle Xiaomi](https://www.mi.com/global/support/faq/details/KA-600067/)

## Pořadí dodání

Fyzické ověření v bodech 1 a 2 proběhne samostatně podle [plánu pilotu C201](C201-STANDALONE-PLAN.md). Při přechodu k bodu 3 už musí být doložený snímek, deset minut živého obrazu, obnova a fyzický PTZ na C201.

1. **Prokázat přenos z C201.** Na Windows ve stejné síti jako kamera ověřit přihlášení, discovery, relaci, dekódování, deset minut živého obrazu a obnovu v samostatném nástroji. Zaznamenat přímou LAN, relay nebo neurčenou trasu podle naměřených dat. Po neúspěchu určit poslední potvrzenou fázi a podle ní vybrat další omezený pokus. Po úspěchu zopakovat zkoušky ve zvoleném cílovém prostředí na Linuxu. [Připnuté revize a licence dalších kandidátů](ADR-002-MANAGED-BACKEND.md)
2. **Nezávisle prokázat posun.** Zjistit skutečnou řídicí cestu pro tento model a firmware. S dohledem otestovat jeden bezpečný krok v každém směru a zastavení, pokud protokol nabízí souvislý pohyb. Úspěch API doložit změnou záběru nebo pozorovaným pohybem. Obraz a ovládání mohou používat různé rozhraní uvnitř téže naší služby. Výběr backendu uzavřít až po ověření obou cest a kontrole licence pro zabalení.
3. **Zapojit fyzickou kameru přes běžné přidání v HA.** Dokončit smlouvu v2, stabilní identitu, chráněný přístup k médiím, bezpečné párování a uložení relace. Uzavřít rozpracované změny inventáře pomocí mock testů včetně starších v1 konfigurací. Zapojit ověřený backend, přihlášení k Xiaomi, výběr kamer, náhled a obnovení přihlášení. Na čistém testovacím HA přidat C201 a ověřit obraz i posun v integraci z tohoto repozitáře. [Technické úkoly T02–T09](SIMPLE-STABLE-PLAN.md)
4. **Dodat další funkce podle tabulky.** Každá funkce je samostatná změna. Zahrne zjištění schopnosti, entitu nebo akci v HA, test selhání a důkaz účinku na C201. U funkcí vyžadujících další službu zaznamenat závislost dřív, než se objeví v návodu nebo UI.
5. **Prověřit stabilitu a připravit vydání.** Spustit Go race testy, vet, klientské testy a čistý HA běh včetně ONVIF regrese. Na C201 změřit desetiminutové video, deset řízených cyklů výpadku a obnovy a 72hodinový běh. Cíle na zdravé referenční síti jsou první snímek do 15 sekund, signalizace přerušení do 30 sekund a obnova do 120 sekund od návratu sítě. Zkoušky zopakovat pro každou platformu uváděnou jako podporovanou. Doložit čistou instalaci, aktualizaci i návrat na předchozí verzi se zachováním dat. [Technické úkoly T10–T11](SIMPLE-STABLE-PLAN.md)

## Podmínka předání

Výsledek je připravený k předání, až projdou všechny povinné testy a uzavře se stav všech dalších funkcí v dohodnutém rozsahu. Záznam uvede model, firmware, region, revize, testovací prostředí, měření a výsledek každé funkce. Termín vydání má smysl odhadnout po ověření prvního obrazu a fyzického posunu. Oba výsledky jsou v tuto chvíli otevřené.

Diagnostika stávajícího pilotu lokalizovala selhání na `DESCRIBE 404` bez SDP; přesná příčina v Xiaomi/go2rtc zdroji zůstává otevřená. Verzované úložiště identity/výběru je zapojené v explicitním managed startu; autorizované `/v2/info` vrací stabilní identitu a `PUT /v2/selection` trvale ukládá výběr s revizí. Další produktová brána je prokázat dekódovaný snímek na C201 dříve, než se vybere a zabalí backend. Neoznačit C201 za podporovanou, dokud neprojde skutečné video a fyzický pohyb.
