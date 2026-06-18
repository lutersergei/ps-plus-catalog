# PS Store — находки разведки (Task 2)

Дата: 2026-06-18. Регион: tr-TR.

## ✅ ВЫБРАННЫЙ ИСТОЧНИК КАТАЛОГА: gameslist (imagic)

Чистый публичный JSON со всем каталогом PS Plus, без хэшей/whitelist/авторизации:

```
GET https://www.playstation.com/bin/imagic/gameslist?locale=tr-tr&categoryList=plus-games-list
```

- Заголовки: обычный `user-agent` + `referer: https://www.playstation.com/tr-tr/ps-plus/games/`.
  Куки/Akamai НЕ обязательны (проверено).
- Ответ: массив групп по алфавиту: `[{catalogKey,count,games:[...]}]` (27 групп, ~469 игр).
- Поле игры:
  - `conceptId` (int) — id концепта, ключ;
  - `name` (локализованное), **`nameEn`** (английское — для матчинга оценок);
  - `conceptUrl` — ссылка на стор;
  - `imageUrl` — обложка;
  - `genre` — список (есть дубли, нужен дедуп), напр. `["ADVENTURE","ACTION"]`;
  - `releaseDate` — ISO8601 (`2017-12-05T21:00:00Z`), год берётся из него;
  - `device` — список платформ (`["PS4","PS5"]`);
  - `productId`, `ageRating`, `streamingSupported`.
- Пропусков по ключевым полям нет (проверено на всех 469).
- `categoryList=plus-games-list` = весь каталог PS Plus (Extra). Возможны и другие
  значения (classics/trials) — для Extra-каталога нужен `plus-games-list`.

Фикстуры: `testdata/gameslist_full.json` (реальный ответ), `testdata/gameslist_sample.json`
(урезанный для тестов парсера).

Это заменяет GraphQL-маршрут ниже (тот оставлен как справка/запасной вариант для
доп. данных по концепту, если понадобится).

---

## (Справка) GraphQL categoryGridRetrieve

## Рабочий вызов categoryGridRetrieve

Эндпоинт принимает **только whitelisted persisted-запросы** (на произвольный
query — `"Query not whitelisted"`). Рабочий вызов (проверен, отдаёт данные):

```
GET https://web.np.playstation.com/api/graphql/v1/op
  ?operationName=categoryGridRetrieve
  &variables=<urlenc JSON>
  &extensions=<urlenc {"persistedQuery":{"version":1,"sha256Hash":"<HASH>"}}>
```

- **sha256Hash (categoryGridRetrieve):**
  `4e41660b6732f35c99fc5541926b7502a09557924e8c2cfebd1beb1a5c8c8f81`
  (app-хэш; меняется только при смене текста запроса самим PlayStation — редко.)
- **variables:** `{"id":"<categoryUUID>","pageArgs":{"size":24,"offset":N},"sortBy":null,"filterBy":[],"facetOptions":[...],"maxResults":null}`

### Обязательные заголовки

- `apollo-require-preflight: true` (или `x-apollo-operation-name: categoryGridRetrieve`) —
  иначе `CSRF_ERROR`.
- `x-psn-store-locale-override: tr-TR`
- `origin: https://store.playstation.com`, `referer: https://store.playstation.com/`
- обычный браузерный `user-agent`
- `accept-encoding: gzip` — ответ приходит сжатым (curl: `--compressed`).
- Куки/Akamai-токены НЕ обязательны для серверного вызова (проверено без cookie).

## Форма ответа

`data.categoryGridRetrieve`:
- `localizedName` — служебное имя категории (напр. `cat.gma.x_All_games`).
- `pageInfo { totalCount, offset, size, isLast }` — пагинация.
- `concepts[]` — { id, name, media[] {type,url,role}, price, products[]{id}, telemetryData }.
- `products[]` — { id, name, platforms, localizedStoreDisplayClassification, media[] } (для категорий продуктов).
- `facetOptions[]` — глобальные фасеты-фильтры (в т.ч. `conceptGenres` со списком жанров,
  `targetPlatforms`), НО **только как фильтры**, не как поля по каждой игре.

### Ограничение

Грид НЕ возвращает жанр и год по каждой игре — только глобальный список жанров в
фасетах. Для года/жанра по конкретной игре нужен отдельный запрос детали концепта
(другой persisted-хэш, пока не снят).

## Известные категории TR (localizedName | totalCount)

Получены опросом UUID со страниц browse/subscriptions:

- `28c9c2b2-cecc-415c-9a08-482a605cb104` — cat.gma.x_All_games | 10000
- `30e3fe35-8f2d-4496-95bc-844f56952e3c` — cat.gma.x_All_PS4_games | 10000
- `d0446d4b-dc9a-4f1e-86ec-651f099c9b29` — cat.gma.x_All_PS5_games | 7013
- `4dfd67ab-...` — FreeToPlay | 227; `7b0ad209-...` — PlayStation_VR2_Games | 390
- `db65f8d8-...` — UbisoftPlus | 66; `2ff6eb11-...` — EA_Play | 3
- `defeaa4c-...` — PS5_Pro_Enhanced_Games | 55; `74d4e266-...` — x_Explore_The_Play_List | 59

## Блокер

Категория **PS Plus Extra (Game Catalog) НЕ найдена** среди категорий стора на
страницах browse/subscriptions. Список каталога рендерится отдельным
микрофронтендом playstation.com; его запрос без браузера локализовать не удалось.
Нужен либо UUID категории PS Plus Game Catalog (тогда categoryGridRetrieve + наш
хэш закрывают всё), либо снятый из браузера запрос со списком игр каталога.
