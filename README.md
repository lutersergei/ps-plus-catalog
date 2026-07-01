# ps-extra

Каталог игр подписки **PlayStation Plus Extra (регион Турция)** с оценками
**Metacritic**, **OpenCritic** и временем прохождения **HowLongToBeat**, с
веб-страницей: пагинация, фильтры по году, жанру, средней оценке и времени
прохождения, сортировка по году / средней оценке / названию / времени прохождения.

## Как это устроено

Одна Go-программа с двумя подкомандами:

- **`sync`** — тянет каталог PS Plus из публичного JSON
  (`playstation.com/bin/imagic/gameslist`), добирает оценки Metacritic (скрейп
  страницы игры) и OpenCritic (RapidAPI) и пишёт всё в SQLite. Оценки кэшируются:
  повторный запуск не перезапрашивает свежие.
- **`serve`** — поднимает локальный HTTP-сервер, читает из SQLite и рендерит
  страницу. Фильтры/сортировка/пагинация работают серверным SQL через
  query-параметры.

## Как собираются внешние оценки

### Metacritic

```text
sync
  |
  v
1. Берём игры, где mc_checked_at пустой или старше -refresh-days
  |
  v
2. Строим slug из очищенного английского названия:
   https://www.metacritic.com/game/{slug}/
  |
  v
3. Загружаем страницу игры
   404 -> считаем, что игры под этим slug нет
   5xx/сеть -> не помечаем проверенной, повторим позже
  |
  v
4. Достаём critic score:
   JSON-LD aggregateRating.name == "Metascore"
   fallback: текст "Metascore N out of 100"
  |
  v
5. Достаём user score:
   ищем в HTML canonical backend URL
   https://backend.metacritic.com/reviews/metacritic/user/games/{canonical_slug}/stats/web...
   если URL не найден -> fallback на исходный slug
  |
  v
6. User score переводим из 0-10 в 0-100:
   7.8 -> 78
  |
  v
7. Сохраняем:
   metacritic_score, metacritic_user_score, metacritic_user_count, mc_checked_at
```

User score не влияет на `average_score`: пока он только хранится. Ошибка при
получении user score не отменяет сохранение critic score.

### OpenCritic

```text
sync
  |
  v
1. Собираем ключи RapidAPI:
   OPENCRITIC_API_KEYS + OPENCRITIC_API_KEY
   ключей нет -> OpenCritic пропускается
  |
  v
2. Берём игры, где oc_checked_at пустой или старше -refresh-days
   лимит за запуск: -max-oc * число ключей
  |
  v
3. /game/search?criteria={clean_title}
   выбираем best match:
   точное нормализованное имя или близкий dist + проверка токенов
  |
  v
4. /game/{opencritic_id}
   достаём topCriticScore и canonical url
  |
  v
5. Если задан OPENCRITIC_SITE_API_KEY:
   https://api.opencritic.com/api/ratings/game/{opencritic_id}
   Authorization: Bearer {OPENCRITIC_SITE_API_KEY}
   median -> opencritic_player_score
   count  -> opencritic_player_count
  |
  v
6. Сохраняем:
   opencritic_score, opencritic_id, opencritic_url,
   opencritic_player_score, opencritic_player_count, oc_checked_at
```

При `429` текущий RapidAPI-ключ помечается исчерпанным и используется следующий.
Если все ключи исчерпаны, OpenCritic-часть останавливается до следующего запуска.
`median:null, count:0` означает, что Player Rating у OpenCritic отсутствует.

### HowLongToBeat

```text
sync
  |
  v
1. Берём игры, где hltb_checked_at пустой или старше -refresh-days
   -max-hltb ограничивает размер пачки (0 = без ограничения)
  |
  v
2. GET /api/bleed/init
   получаем token + honeypot key/value
  |
  v
3. POST /api/bleed
   пробуем несколько вариантов запроса:
   полное очищенное название -> первые 3 слова -> первые 2 слова
  |
  v
4. Выбираем best match:
   точное нормализованное имя или совпадение значимых токенов
  |
  v
5. Сохраняем:
   hltb_main_extra = comp_plus
   hltb_rating = review_score
   hltb_id = game_id
   hltb_url = https://howlongtobeat.com/game/{game_id}
   hltb_checked_at
```

Если HLTB вернул непустую выдачу, но нужной игры нет, это кэшируется как
достоверное отсутствие. Если все варианты дали пустую выдачу, строка не
помечается проверенной: это часто троттлинг, игра повторится в следующем sync.

## Требования

- Go 1.25+
- Зависимости тянутся через прокси Go. Если корпоративный прокси недоступен:
  ```sh
  GOPROXY=https://proxy.golang.org,direct go mod download
  ```

## Сборка

```sh
go build -o ps-extra .
```

## Запуск

### 1. Собрать данные

Ключи OpenCritic (RapidAPI) задаются через окружение или файл `.env` — в коде их нет.
Скопируйте шаблон и впишите свои ключи:

```sh
cp .env.example .env
# в .env: OPENCRITIC_API_KEYS=key1,key2,key3   (несколько — через запятую)
./ps-extra sync                 # каталог + оценки в ps-extra.db
```

**Несколько ключей**: их дневные квоты суммируются — при ответе `429` на одном
ключе `sync` автоматически переходит к следующему. Можно задать и одиночный
`OPENCRITIC_API_KEY`. Реальные переменные окружения имеют приоритет над `.env`.
Для сохранения OpenCritic Player Rating дополнительно задайте
`OPENCRITIC_SITE_API_KEY` — только bearer-часть токена сайта, без префикса
`Bearer `.

Без ключей соберётся каталог и только Metacritic + HowLongToBeat:

```sh
./ps-extra sync
```

Флаги `sync`:

| флаг | по умолчанию | назначение |
|---|---|---|
| `-db` | `ps-extra.db` | путь к файлу SQLite |
| `-skip-scores` | `false` | обновить только каталог, без оценок |
| `-max-oc` | `25` | лимит игр OpenCritic **на каждый ключ** за запуск (суммарно ×кол-во ключей) |
| `-max-hltb` | `0` | лимит игр HowLongToBeat за запуск (0 = без лимита) |
| `-recheck-missing` | `false` | перепроверить игры без оценки |
| `-refresh-days` | `30` | не перезапрашивать оценки свежее N дней |
| `-allow-shrink` | `false` | применить снимок каталога, даже если он намного меньше текущего (иначе резкое сжатие считается частичным ответом и прерывает sync) |

> **Про лимиты OpenCritic.** Бесплатный план RapidAPI — **25 поисков/день на ключ**.
> За запуск собираются оценки максимум для `-max-oc` × (число ключей) игр, остальные
> подтянутся при следующих запусках (кэш по `oc_checked_at`). Metacritic и HLTB не
> лимитированы ключом и собираются для всех обрабатываемых за запуск игр.

### 2. Показать страницу

```sh
./ps-extra serve                      # http://localhost:8080 (только localhost)
./ps-extra serve -addr 127.0.0.1:9000 # другой порт
./ps-extra serve -addr :8080          # слушать на всех интерфейсах (внешний доступ)
```

Откройте `http://localhost:8080` в браузере.

> По умолчанию сервер слушает только `127.0.0.1` (локально). Для доступа извне
> задайте `-addr :8080` и поставьте перед сервисом reverse proxy с TLS — в
> приложении нет аутентификации.

## Docker

```sh
cp .env.example .env            # впишите ключи RapidAPI (можно несколько)

# контейнер работает под UID 65532 (distroless nonroot) — создайте папку заранее:
mkdir -p data && chown 65532:65532 data

# веб-сервер на http://localhost:8080, БД хранится в ./data на хосте
docker compose up -d --build

# сбор данных (каталог + оценки) — отдельный одноразовый запуск:
docker compose run --rm ps-extra sync
```

Образ — многостадийный (статический бинарь на distroless, без CGO). Том `./data`
хранит `ps-extra.db` между перезапусками; ключи берутся из `.env` (через `env_file`).

Без compose:

```sh
docker build -t ps-extra .
docker run --rm -v "$PWD/data:/data" --env-file .env ps-extra sync   # собрать
docker run --rm -p 8080:8080 -v "$PWD/data:/data" ps-extra            # показать
```

## Структура

```
main.go                     CLI-диспетчер (sync | serve)
sync.go                     команда sync: каталог + сбор оценок
serve.go                    команда serve: HTTP-хендлер
templates/index.html        страница (встроена через go:embed)
internal/psstore/           клиент и парсер каталога PS Plus
internal/scores/            провайдеры Metacritic / OpenCritic (пул ключей) / HowLongToBeat
internal/store/             SQLite: схема, запись, выборка для отображения
internal/envfile/           чтение .env (несколько ключей RapidAPI)
Dockerfile, docker-compose.yml, .env.example
docs/research/              находки по источникам данных (эндпоинты, форматы)
testdata/                   фикстуры реальных ответов
```

## Заметки

- Сопоставление игр с оценками — по английскому названию (`nameEn`) с чисткой
  платформенных/издательских суффиксов. Совпадение неточное: часть игр останется
  без оценки (в UI — «—»), такие случаи логируются.
- Первичный ключ БД — `productId` из API PS Store. Если у вас есть старая база
  данных, собранная до этого изменения, удалите её и запустите `sync` заново:
  имевшиеся там `conceptId`-ключи несовместимы со схемой.
