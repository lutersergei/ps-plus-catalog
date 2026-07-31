# ps-extra

Каталог игр подписки **PlayStation Plus Extra (регион Турция)** с оценками
**Metacritic**, **OpenCritic** и временем прохождения **HowLongToBeat**, с
веб-страницей: пагинация, фильтры по году, жанру, общей оценке, оценкам критиков,
оценкам игроков и времени прохождения, сортировка по году / оценкам / названию /
времени прохождения. Пользователь может войти через Google, добавить игры в
персональное избранное и открыть отдельную отфильтрованную ленту избранных игр.

## Как это устроено

Одна Go-программа с тремя подкомандами:

- **`sync`** — тянет каталог PS Plus из публичного JSON
  (`playstation.com/bin/imagic/gameslist`), добирает оценки Metacritic (скрейп
  страницы игры) и OpenCritic (RapidAPI) и пишёт всё в SQLite. Оценки кэшируются:
  повторный запуск не перезапрашивает свежие.
- **`serve`** — поднимает локальный HTTP-сервер, читает из SQLite и рендерит
  страницу. Фильтры/сортировка/пагинация работают серверным SQL через
  query-параметры; при настроенном Google OAuth также обслуживает вход, сессии и
  персональное избранное.
- **`sync-dates`** — отдельно обновляет даты появления игр по официальным
  анонсам PlayStation Blog и проверенному историческому манифесту.

## Как собираются внешние оценки

### Metacritic

```text
sync
  |
  v
1. Берём игры, где mc_checked_at пустой или старше -refresh-days
  |
  v
2. Пробуем прямые slug-кандидаты:
   - из исходного английского названия
   - из очищенного английского названия
   https://www.metacritic.com/game/{slug}/
  |
  v
3. Если прямые slug-и не сработали, используем поиск Metacritic:
   https://www.metacritic.com/search/{title}/
   кандидаты проверяются консервативным title matching
   неоднозначные совпадения пропускаются
  |
  v
4. Загружаем страницу игры
   404 -> пробуем следующий slug/кандидат
   5xx/сеть -> не помечаем проверенной, повторим позже
  |
  v
5. Достаём critic score:
   JSON-LD aggregateRating.name == "Metascore"
   fallback: текст "Metascore N out of 100"
  |
  v
6. Достаём user score:
   ищем в HTML canonical backend URL
   https://backend.metacritic.com/reviews/metacritic/user/games/{canonical_slug}/stats/web...
   если URL не найден -> fallback на исходный slug
  |
  v
7. User score переводим из 0-10 в 0-100:
   7.8 -> 78
  |
  v
8. Сохраняем:
   metacritic_score, metacritic_url,
   metacritic_user_score, metacritic_user_count, mc_checked_at
```

Ошибка при получении user score не отменяет сохранение critic score.
`metacritic_url` используется в UI как canonical-ссылка на найденную страницу.

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

- Go 1.25.12+
- Зависимости тянутся через прокси Go. Если корпоративный прокси недоступен:
  ```sh
  GOPROXY=https://proxy.golang.org,direct go mod download
  ```

## Сборка

```sh
go build -o ps-extra ./cmd/ps-extra
```

Для запуска без промежуточной сборки используйте тот же пакет команды:

```sh
go run ./cmd/ps-extra serve -addr 127.0.0.1:8080
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

Каждый успешный sync также обновляет историю присутствия игр в таблице
`catalog_memberships`. В исходном снимке дата добавления остаётся неизвестной:
сам факт наличия игры не доказывает, что она добавлена в день запуска sync.
Новые и вернувшиеся игры получают дату первого наблюдения и источник
`observed`; отдельный период сохраняется при каждом возвращении игры.

У публичного JSON PlayStation нет поля с датой включения в PS Plus. Точную дату
`sync` автоматически получает из архива официальных анонсов PlayStation Blog и
записывает с источником `announcement`. Для Турции используется глобальная дата
или дата `all other regions`, а не отдельное расписание США, Великобритании и
Японии. Разобранные статьи кэшируются в SQLite и повторно скачиваются только при
изменении sitemap или версии парсера. `releaseDate` из JSON — дата релиза самой
игры, не дата добавления в каталог.

Только даты, без обновления каталога, оценок, HLTB и языков:

```sh
./ps-extra sync-dates
./ps-extra sync-dates -verbose # перечислить неоднозначные и ненайденные игры
./ps-extra sync-dates -refresh # принудительно перечитать все статьи
```

Игры, которых нет в официальных анонсах или которые нельзя сопоставить
однозначно, остаются без даты. Интерфейс сортирует их после игр с известной
датой.

Флаги `sync`:

| флаг | по умолчанию | назначение |
|---|---|---|
| `-db` | `ps-extra.db` | путь к файлу SQLite |
| `-skip-scores` | `false` | обновить только каталог, без оценок |
| `-max-oc` | `25` | лимит игр OpenCritic **на каждый ключ** за запуск (суммарно ×кол-во ключей) |
| `-max-hltb` | `0` | лимит игр HowLongToBeat за запуск (0 = без лимита) |
| `-max-langs` | `0` | лимит игр для сбора языков PS Store (0 = без лимита) |
| `-recheck-missing` | `false` | перепроверить игры без оценки |
| `-refresh-days` | `30` | не перезапрашивать оценки свежее N дней |
| `-allow-shrink` | `false` | применить снимок каталога, даже если он намного меньше текущего (иначе резкое сжатие считается частичным ответом и прерывает sync) |

Флаги `sync-dates`:

| флаг | по умолчанию | назначение |
|---|---|---|
| `-db` | `ps-extra.db` | путь к файлу SQLite |
| `-refresh` | `false` | заново скачать все анонсы, игнорируя кэш |
| `-verbose` | `false` | перечислить игры без однозначного совпадения |

> **Про лимиты OpenCritic.** Бесплатный план RapidAPI — **25 поисков/день на ключ**.
> За запуск собираются оценки максимум для `-max-oc` × (число ключей) игр, остальные
> подтянутся при следующих запусках (кэш по `oc_checked_at`). Metacritic и HLTB не
> лимитированы ключом и собираются для всех обрабатываемых за запуск игр.

### Авторизация Google и избранное

Авторизация опциональна: без Google-переменных каталог продолжает работать
публично, но кнопки входа и избранного не показываются. Если задана только часть
настроек, `serve` завершится с понятной ошибкой вместо запуска сломанного OAuth.

1. В Google Cloud Console настройте OAuth consent screen.
2. Создайте OAuth client типа **Web application**.
3. Добавьте точный Authorized redirect URI:

```text
# локальная разработка
http://localhost:8080/auth/google/callback

# production под внешним префиксом /games
https://slyuter.store/games/auth/google/callback
```

4. Добавьте в `.env`:

```dotenv
GOOGLE_CLIENT_ID=1234567890-example.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=google_oauth_client_secret
PS_EXTRA_PUBLIC_URL=http://localhost:8080
```

Для production используйте
`PS_EXTRA_PUBLIC_URL=https://slyuter.store/games`. Внешний URL обязан работать
по HTTPS: HTTP разрешён кодом только для `localhost`/loopback, а Google не
принимает обычный внешний HTTP callback. Reverse proxy должен снимать префикс
`/games`: внутри контейнера callback обслуживается как `/auth/google/callback`.

Приложение запрашивает только scopes `openid email profile`. Access token Google
используется один раз для `userinfo` и не сохраняется. В SQLite записываются
Google `sub`, профиль пользователя, SHA-256 случайного session token и избранные
`productId`; сама session cookie — `HttpOnly`, `SameSite=Lax`, а на HTTPS также
`Secure`. Сессия живёт 30 дней. Изменение избранного и выход дополнительно
защищены CSRF-токеном.

### Миграции SQLite

Миграции встроены в бинарник и автоматически выполняются перед любой командой,
которая открывает БД: `serve`, `sync` или `sync-dates`. Отдельная команда для
миграции production не требуется. Применённые версии записываются в таблицу
`schema_migrations` вместе с именем и SHA-256 исходного SQL.

Перед обновлением приложения сделайте online-backup SQLite:

```sh
sqlite3 ps-extra.db ".backup 'ps-extra.db.bak'"
```

Порядок обновления production:

1. Создать backup БД.
2. Обновить бинарник или Docker image.
3. Запустить один экземпляр приложения — он применит ожидающие миграции одной
   транзакцией до начала обслуживания запросов.
4. Проверить логи запуска и открыть каталог.

Миграции являются forward-only: автоматического downgrade нет. Для отката на
бинарник со старой схемой восстановите backup. Уже применённые SQL-файлы нельзя
редактировать: несовпадение checksum или неизвестная более новая версия заставят
приложение безопасно отказаться от запуска. Новая миграция добавляется отдельным
файлом вида `internal/adapters/sqlite/migrations/0003_name.sql`.

При первом запуске версия `0001_baseline` принимает существующую БД актуального
формата (с ключами `productId`) без потери данных и добавляет только отсутствующие
исторические столбцы. Каталог с файлом БД должен оставаться доступным на запись
для SQLite WAL/SHM.

Миграция `0002_google_auth_favorites` добавляет таблицы `users`, `user_sessions`
и `user_favorites`. Она не меняет каталог и безопасно применяется до запуска
HTTP-сервера.

### 2. Показать страницу

```sh
./ps-extra serve                      # http://localhost:8080 (только localhost)
./ps-extra serve -addr 127.0.0.1:9000 # другой порт
./ps-extra serve -addr :8080          # слушать на всех интерфейсах (внешний доступ)
```

Откройте `http://localhost:8080` в браузере.

> По умолчанию сервер слушает только `127.0.0.1` (локально). Для доступа извне
> задайте `-addr :8080` и поставьте перед сервисом reverse proxy с TLS. Вход
> защищает персональные функции, но сам каталог намеренно остаётся публичным.

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

### Production Compose

[`docker-compose.prod.yml`](docker-compose.prod.yml) is the source of truth
for the production service configuration. It contains no credentials: keep
`.env` and `data/` only on the production host.

The production host runs the manifest from `/opt/ps-extra` as
`docker-compose.yml`. When deploying a manifest change, copy the versioned file
there before pulling the image:

```sh
scp -P 22222 docker-compose.prod.yml root@<host>:/opt/ps-extra/docker-compose.yml
ssh -p 22222 root@<host> 'cd /opt/ps-extra && docker compose pull ps-extra && docker compose up -d ps-extra'
```

Mount `./data:/data`, not only `ps-extra.db`: SQLite WAL and SHM sidecar files
must persist alongside the database after one-off `sync` containers exit.

## Структура

```
cmd/ps-extra/main.go            исполняемая точка и запуск приложения
assets.go                       встраивание корневых HTML/JSON-ресурсов для cmd
templates/index.html            неизменяемый HTML-контракт (go:embed)
internal/app/                    CLI, конфигурация и ручная композиция зависимостей
internal/domain/                 независимые структуры и чистые правила сопоставления
internal/services/               сценарии просмотра, синхронизации и дат каталога
internal/handlers/               HTTP-разбор, view model и рендеринг шаблона
internal/adapters/psstore/       HTTP-клиент и парсеры PlayStation
internal/adapters/scores/        Metacritic, OpenCritic и HowLongToBeat
internal/adapters/googleauth/    Google OAuth 2.0 и OpenID Connect userinfo
internal/adapters/sqlite/        миграции, транзакции и запросы SQLite
internal/infrastructure/envfile/ чтение локального .env
Dockerfile, docker-compose.yml, .env.example
docs/research/              находки по источникам данных (эндпоинты, форматы)
testdata/                   фикстуры реальных ответов
```

Поток вызовов backend-кода: `handlers → services → adapters`. Компиляционные
зависимости слоёв сходятся в `domain`, а интерфейсы объявлены потребителями в
`handlers` и `services`. Конкретные реализации вручную собираются в
`internal/app`. Исполняемый пакет находится в `cmd/ps-extra`; корневой
`assets.go` нужен только из-за ограничения `go:embed`, запрещающего встраивать
файлы из родительского каталога.

## Заметки

- Сопоставление игр с оценками — по английскому названию (`nameEn`) с чисткой
  платформенных/издательских суффиксов и fallback-поиском по источнику. Совпадение
  консервативное: неоднозначные результаты не записываются, поэтому часть игр
  останется без оценки (в UI — «—»).
- `average_score` считается по доступным ненулевым источникам из пяти: Metacritic
  critic/user, OpenCritic critic/player и HowLongToBeat rating. Отдельно хранятся
  `critic_average_score` и `player_average_score` для фильтров и сортировки.
- Первичный ключ БД — `productId` из API PS Store. Если у вас есть старая база
  данных, собранная до этого изменения, удалите её и запустите `sync` заново:
  имевшиеся там `conceptId`-ключи несовместимы со схемой.
