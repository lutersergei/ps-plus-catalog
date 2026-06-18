# PS Plus Extra (TR) Catalog — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Go-программа, которая собирает каталог PS Plus Extra (Турция) с оценками Metacritic/OpenCritic в SQLite и показывает его на локальной HTML-странице с пагинацией, фильтрами и сортировкой.

**Architecture:** Одна программа с двумя подкомандами. `sync` тянет каталог из GraphQL веб-стора PlayStation, добирает оценки (OpenCritic API + скрейп Metacritic) и пишет в SQLite. `serve` поднимает HTTP-сервер на localhost, читает из SQLite и рендерит HTML; фильтры/сортировка/пагинация — серверным SQL через query-параметры.

**Tech Stack:** Go 1.25, стандартная библиотека (`net/http`, `html/template`, `encoding/json`, `flag`), `modernc.org/sqlite` (чистый Go, без cgo), `golang.org/x/net/html` для скрейпа.

## Global Constraints

- Go 1.25 (`go 1.25` в go.mod).
- SQLite-драйвер: `modernc.org/sqlite` (без cgo, имя драйвера `"sqlite"`).
- RapidAPI-ключ OpenCritic — только через env `OPENCRITIC_API_KEY`, никогда не в коде/репозитории.
- Регион стора: Турция (locale `tr-tr`).
- Тесты по решению пользователя откладываются до рабочего MVP; код разбит на тестируемые единицы, но шаги верификации в MVP — ручные (curl / просмотр в браузере).
- Все сетевые HTTP-запросы идут с реалистичным `User-Agent` и таймаутом.

---

### Task 1: Каркас проекта и CLI

**Files:**
- Create: `go.mod`
- Create: `main.go`
- Create: `internal/store/db.go`

**Interfaces:**
- Produces: `func store.Open(path string) (*sql.DB, error)` — открывает SQLite и применяет миграции; `func store.Migrate(db *sql.DB) error`.
- Produces: `main` понимает подкоманды `sync` и `serve` через `flag`/`os.Args`.

- [ ] **Step 1: Инициализация модуля**

```bash
cd /Users/slyuter/projects/ps-extra
go mod init ps-extra
go get modernc.org/sqlite
go get golang.org/x/net/html
```

- [ ] **Step 2: Схема БД и миграции (`internal/store/db.go`)**

```go
package store

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS games (
	id                TEXT PRIMARY KEY,
	title             TEXT NOT NULL,
	release_year      INTEGER,
	platforms         TEXT,
	image_url         TEXT,
	store_url         TEXT,
	metacritic_score  INTEGER,
	opencritic_score  INTEGER,
	average_score     REAL,
	scores_updated_at TIMESTAMP
);
CREATE TABLE IF NOT EXISTS game_genres (
	game_id TEXT NOT NULL,
	genre   TEXT NOT NULL,
	PRIMARY KEY (game_id, genre)
);
CREATE INDEX IF NOT EXISTS idx_game_genres_genre ON game_genres(genre);
`

func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := Migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func Migrate(db *sql.DB) error {
	_, err := db.Exec(schema)
	return err
}
```

- [ ] **Step 3: CLI-диспетчер (`main.go`)**

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ps-extra <sync|serve> [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "sync":
		if err := runSync(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "sync error:", err)
			os.Exit(1)
		}
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "serve error:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", os.Args[1])
		os.Exit(2)
	}
}
```

- [ ] **Step 4: Заглушки команд, чтобы собиралось (`main.go` дополнить)**

```go
func runSync(args []string) error { fmt.Println("sync: not implemented yet"); return nil }
func runServe(args []string) error { fmt.Println("serve: not implemented yet"); return nil }
```

- [ ] **Step 5: Сборка и проверка**

Run: `go build ./... && ./ps-extra` (без аргументов)
Expected: печатает usage и выходит с кодом 2; `./ps-extra sync` печатает заглушку.

- [ ] **Step 6: Commit**

```bash
git init && git add -A && git commit -m "feat: project skeleton with CLI and sqlite schema"
```

---

### Task 2: Спайк — реальный GraphQL-эндпоинт каталога PS Plus Extra (TR)

Цель спайка: установить фактический URL эндпоинта, нужные заголовки, ID категории PS Plus Extra (TR) и форму JSON-ответа. Без этого парсер писать вслепую нельзя.

**Files:**
- Create: `docs/research/ps-store-graphql.md` (заметки + сохранённый пример ответа)
- Create: `testdata/ps_store_response.json` (реальный ответ для будущих фикстур)

**Interfaces:**
- Produces: задокументированные `endpointURL`, требуемые HTTP-заголовки, `categoryID` PS Plus Extra (TR), и путь в JSON до массива игр + полей (title, год, жанры, платформы, изображение, product/concept id).

- [ ] **Step 1: Найти эндпоинт и категорию**

Открыть в браузере `https://store.playstation.com/tr-tr/category/<...>` для PS Plus Extra (Game Catalog), в DevTools → Network отфильтровать XHR к домену с `graphql`. Скопировать: URL, query hash (persisted query) или тело запроса, заголовки (`x-psn-store-locale-override`, `content-type`, и т.п.), и categoryId/conceptId.

- [ ] **Step 2: Воспроизвести запрос через curl**

Собрать рабочий `curl`, отдающий JSON с играми (с пагинацией — посмотреть, как стор просит следующую страницу: offset/size). Сохранить пример ответа в `testdata/ps_store_response.json`.

- [ ] **Step 3: Записать findings в `docs/research/ps-store-graphql.md`**

Зафиксировать: endpoint URL, метод, заголовки, переменные запроса (categoryId, размер страницы, offset), путь в JSON до игр и до полей. Описать механизм пагинации (общее число, как итерировать).

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "research: PS Store GraphQL endpoint for PS Plus Extra TR"
```

---

### Task 3: Клиент PS Store и парсер каталога

**Files:**
- Create: `internal/psstore/client.go`
- Create: `internal/psstore/parse.go`

**Interfaces:**
- Consumes: findings и `testdata/ps_store_response.json` из Task 2.
- Produces:
  - `type Game struct { ID, Title string; ReleaseYear int; Genres, Platforms []string; ImageURL, StoreURL string }`
  - `func psstore.FetchCatalog(ctx context.Context, httpClient *http.Client) ([]Game, error)` — итерирует все страницы категории PS Plus Extra (TR) и возвращает полный список.
  - `func psstore.parsePage(raw []byte) (games []Game, total int, err error)` — чистая функция парсинга одной страницы (для будущих тестов на фикстуре).

- [ ] **Step 1: Типы и парсер (`internal/psstore/parse.go`)**

Реализовать `parsePage`, разбирающую JSON по путям, зафиксированным в Task 2. Жанры/платформы извлекать в `[]string`, год — из даты релиза (год как int). Заполнить `StoreURL` как `https://store.playstation.com/tr-tr/product/<id>` (или concept — по факту из спайка).

```go
package psstore

import "encoding/json"

type Game struct {
	ID          string
	Title       string
	ReleaseYear int
	Genres      []string
	Platforms   []string
	ImageURL    string
	StoreURL    string
}

// parsePage разбирает один ответ GraphQL. Пути к полям — из docs/research/ps-store-graphql.md.
func parsePage(raw []byte) (games []Game, total int, err error) {
	// TODO(impl): структуры под конкретный JSON из Task 2; здесь подставить реальные json-теги.
	var resp struct{ /* заполняется по фактической форме ответа */ }
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, 0, err
	}
	// ...маппинг resp -> []Game, total
	return games, total, nil
}
```

> Примечание исполнителю: точные `json`-теги и пути берутся из `docs/research/ps-store-graphql.md` и проверяются на `testdata/ps_store_response.json`. Это не placeholder — форма известна только после Task 2.

- [ ] **Step 2: Клиент с пагинацией (`internal/psstore/client.go`)**

`FetchCatalog` создаёт запросы по страницам (offset += size, пока собрано < total), вызывает `parsePage`, аккумулирует игры. Заголовки и URL — из Task 2. Таймаут на `http.Client` (30s), реалистичный `User-Agent`, троттлинг ~300мс между страницами.

- [ ] **Step 3: Проверка через временный main-хук**

Временно в `runSync` вызвать `FetchCatalog` и напечатать `len(games)` и первые 3 названия.

Run: `go run . sync`
Expected: печатает ненулевое число игр и осмысленные названия из каталога TR.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "feat: PS Store catalog client and parser"
```

---

### Task 4: Запись каталога в SQLite (команда sync, часть 1)

**Files:**
- Create: `internal/store/games.go`
- Modify: `main.go` (реализовать `runSync` поверх заглушки)
- Create: `sync.go`

**Interfaces:**
- Consumes: `psstore.FetchCatalog`, `store.Open`.
- Produces:
  - `func store.UpsertGame(db *sql.DB, g GameRow) error`
  - `type store.GameRow struct { ID, Title string; ReleaseYear int; Genres, Platforms []string; ImageURL, StoreURL string }`
  - `func store.SetGenres(db *sql.DB, gameID string, genres []string) error`

- [ ] **Step 1: Запись игр (`internal/store/games.go`)**

`UpsertGame` делает `INSERT ... ON CONFLICT(id) DO UPDATE` по полям каталога (НЕ трогает поля оценок). `SetGenres` — удаляет старые строки `game_genres` для game_id и вставляет новые.

```go
func UpsertGame(db *sql.DB, g GameRow) error {
	_, err := db.Exec(`
INSERT INTO games (id, title, release_year, platforms, image_url, store_url)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  title=excluded.title, release_year=excluded.release_year,
  platforms=excluded.platforms, image_url=excluded.image_url, store_url=excluded.store_url`,
		g.ID, g.Title, g.ReleaseYear, strings.Join(g.Platforms, ", "), g.ImageURL, g.StoreURL)
	return err
}
```

- [ ] **Step 2: Команда sync (`sync.go`)**

`runSync`: флаг `-db` (по умолч. `ps-extra.db`). Открыть БД, `FetchCatalog`, для каждой игры `UpsertGame` + `SetGenres`. Логировать прогресс.

- [ ] **Step 3: Проверка**

Run: `go run . sync -db ps-extra.db`
Then: `sqlite3 ps-extra.db "SELECT count(*) FROM games; SELECT count(DISTINCT genre) FROM game_genres;"`
Expected: число игр > 0, число жанров > 0.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "feat: persist catalog into sqlite via sync"
```

---

### Task 5: Спайк — OpenCritic API и страница Metacritic

**Files:**
- Create: `docs/research/scores-sources.md`
- Create: `testdata/opencritic_search.json`, `testdata/opencritic_game.json`
- Create: `testdata/metacritic_game.html`

**Interfaces:**
- Produces: задокументированные эндпоинты OpenCritic (search + game by id), заголовки RapidAPI, путь к Top Critic Score; URL-шаблон страницы Metacritic и место, где в HTML лежит metascore.

- [ ] **Step 1: OpenCritic через RapidAPI**

С `OPENCRITIC_API_KEY` сделать curl к search-эндпоинту OpenCritic (RapidAPI) по названию игры, затем к game-эндпоинту по id. Сохранить ответы в `testdata/`. Зафиксировать путь к числовому Top Critic Score и формат поиска.

- [ ] **Step 2: Metacritic**

Открыть страницу игры на metacritic.com, сохранить HTML в `testdata/metacritic_game.html`. Определить URL-шаблон (`https://www.metacritic.com/game/<slug>/`) и где находится metascore (атрибут/класс/JSON-LD). Проверить, не отдаёт ли Cloudflare блок при curl.

- [ ] **Step 3: Записать findings в `docs/research/scores-sources.md`**

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "research: OpenCritic API and Metacritic page structure"
```

---

### Task 6: Провайдеры оценок

**Files:**
- Create: `internal/scores/opencritic.go`
- Create: `internal/scores/metacritic.go`
- Create: `internal/scores/normalize.go`

**Interfaces:**
- Consumes: findings/фикстуры из Task 5.
- Produces:
  - `func scores.NormalizeTitle(s string) string` — нижний регистр, удаление символов ™®, скобок-изданий, лишних пробелов.
  - `func scores.OpenCriticScore(ctx context.Context, c *http.Client, apiKey, title string) (int, bool, error)` — возвращает оценку и `found`.
  - `func scores.MetacriticScore(ctx context.Context, c *http.Client, title string) (int, bool, error)`.
  - `func scores.parseOpenCriticGame(raw []byte) (int, error)` и `func scores.parseMetacritic(html []byte) (int, error)` — чистые парсеры под фикстуры.

- [ ] **Step 1: Нормализация названий (`normalize.go`)**

```go
func NormalizeTitle(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("™", "", "®", "", "’", "'").Replace(s)
	// убрать "  - ... edition", двойные пробелы
	return strings.Join(strings.Fields(s), " ")
}
```

- [ ] **Step 2: OpenCritic (`opencritic.go`)**

`OpenCriticScore`: GET к search-эндпоинту (заголовки RapidAPI из Task 5), выбрать лучшее совпадение по `NormalizeTitle`, GET к game-эндпоинту, `parseOpenCriticGame` → Top Critic Score (округлить до int). 429/5xx → backoff (до 3 попыток). Нет совпадения → `found=false`.

- [ ] **Step 3: Metacritic (`metacritic.go`)**

`MetacriticScore`: построить URL из slug (`strings.ReplaceAll(NormalizeTitle(title), " ", "-")`), GET с `User-Agent`, `parseMetacritic` → metascore. Блок Cloudflare/404 → `found=false`, без падения.

- [ ] **Step 4: Проверка парсеров на фикстурах**

Временный `main`-вызов: прогнать `parseOpenCriticGame(testdata/opencritic_game.json)` и `parseMetacritic(testdata/metacritic_game.html)`, напечатать числа.

Run: `go run . <временный флаг проверки>`
Expected: печатает ожидаемые оценки из сохранённых фикстур.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: OpenCritic and Metacritic score providers"
```

---

### Task 7: Сбор оценок в sync (часть 2)

**Files:**
- Modify: `sync.go`
- Modify: `internal/store/games.go`

**Interfaces:**
- Produces:
  - `func store.UpdateScores(db *sql.DB, id string, mc, oc sql.NullInt64, avg sql.NullFloat64) error`
  - `func store.GamesNeedingScores(db *sql.DB, staleBefore time.Time) ([]struct{ ID, Title string }, error)` — игры без `scores_updated_at` или старее порога.
  - `func sync.computeAverage(mc, oc sql.NullInt64) sql.NullFloat64`

- [ ] **Step 1: Запрос игр без свежих оценок (`games.go`)**

`GamesNeedingScores`: `SELECT id, title FROM games WHERE scores_updated_at IS NULL OR scores_updated_at < ?`.

- [ ] **Step 2: Обновление оценок (`games.go`)**

`UpdateScores`: `UPDATE games SET metacritic_score=?, opencritic_score=?, average_score=?, scores_updated_at=CURRENT_TIMESTAMP WHERE id=?`.

- [ ] **Step 3: computeAverage и цикл в sync (`sync.go`)**

После записи каталога: для каждой игры из `GamesNeedingScores` вызвать оба провайдера, посчитать `average_score` (среднее из доступных; обе шкалы 0–100), `UpdateScores`. Троттлинг ~1с между играми. Флаг `-skip-scores` для отладки только каталога. Ошибки провайдеров логировать, не прерывать цикл.

```go
func computeAverage(mc, oc sql.NullInt64) sql.NullFloat64 {
	var sum float64
	var n int
	if mc.Valid { sum += float64(mc.Int64); n++ }
	if oc.Valid { sum += float64(oc.Int64); n++ }
	if n == 0 { return sql.NullFloat64{} }
	return sql.NullFloat64{Float64: sum / float64(n), Valid: true}
}
```

- [ ] **Step 4: Проверка**

Run: `OPENCRITIC_API_KEY=... go run . sync`
Then: `sqlite3 ps-extra.db "SELECT title, metacritic_score, opencritic_score, average_score FROM games WHERE average_score IS NOT NULL LIMIT 5;"`
Expected: у части игр проставлены оценки и средняя.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: collect Metacritic/OpenCritic scores during sync"
```

---

### Task 8: SQL-слой запроса для отображения (фильтры/сортировка/пагинация)

**Files:**
- Create: `internal/store/query.go`

**Interfaces:**
- Produces:
  - `type ListParams struct { Genre string; Year int; Sort string; Order string; Page, PageSize int }`
  - `type GameView struct { ID, Title string; ReleaseYear int; Genres []string; Platforms, ImageURL, StoreURL string; Metacritic, OpenCritic sql.NullInt64; Average sql.NullFloat64 }`
  - `type ListResult struct { Games []GameView; Total, Page, PageSize, TotalPages int }`
  - `func store.ListGames(db *sql.DB, p ListParams) (ListResult, error)`
  - `func store.DistinctYears(db *sql.DB) ([]int, error)`
  - `func store.DistinctGenres(db *sql.DB) ([]string, error)`

- [ ] **Step 1: Реализовать запросы (`query.go`)**

`ListGames` строит WHERE из непустых фильтров (год: `release_year=?`; жанр: `id IN (SELECT game_id FROM game_genres WHERE genre=?)`). `Sort` ∈ {`year`,`average`,`title`} маппится на белый список колонок (`release_year`,`average_score`,`title`); `average_score` сортируется `NULLS LAST` (через `average_score IS NULL, average_score`). Пагинация — `LIMIT ? OFFSET ?`. Отдельный `COUNT(*)` для `Total`. Жанры каждой игры дочитываются одним запросом по выбранным id. `DistinctYears`/`DistinctGenres` — для выпадающих фильтров.

> Безопасность: колонки сортировки только из белого списка (не из пользовательской строки), значения — через плейсхолдеры `?`.

- [ ] **Step 2: Проверка через временный вызов**

Временно вызвать `ListGames` с `Sort:"average", Order:"desc", PageSize:10` и напечатать заголовки и `TotalPages`.

Run: `go run . <временный флаг>`
Expected: 10 игр, отсортированных по убыванию средней оценки, корректный `TotalPages`.

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "feat: sqlite query layer with filters, sorting, pagination"
```

---

### Task 9: HTTP-сервер и HTML-страница (команда serve)

**Files:**
- Create: `serve.go`
- Create: `templates/index.html`

**Interfaces:**
- Consumes: `store.Open`, `store.ListGames`, `store.DistinctYears`, `store.DistinctGenres`, `ListParams`.
- Produces: `runServe(args []string) error` — флаги `-db`, `-addr` (по умолч. `:8080`); хендлер `GET /` рендерит таблицу/сетку игр.

- [ ] **Step 1: Шаблон страницы (`templates/index.html`)**

Сетка/таблица карточек: обложка, название, год, жанры, MC, OC, средняя. Вверху форма (GET) с `<select>` года и жанра (значения из `DistinctYears`/`DistinctGenres`), `<select>` сортировки (year/average/title) и направления. Внизу — пагинация (ссылки prev/next и номера страниц, сохраняющие текущие query-параметры). Минимальный встроенный CSS. Оценки `NULL` → «N/A».

- [ ] **Step 2: Хендлер (`serve.go`)**

`runServe`: `embed` шаблона (`//go:embed templates/index.html`), парсинг query → `ListParams` (дефолты: page=1, pageSize=24, sort=title), вызов `ListGames` + `Distinct*`, рендер. `http.ListenAndServe(addr, nil)`. Логировать адрес при старте.

```go
//go:embed templates/index.html
var indexTmpl string
```

- [ ] **Step 3: Проверка в браузере**

Run: `go run . serve -db ps-extra.db` затем открыть `http://localhost:8080`.
Expected: страница со списком игр; смена года/жанра/сортировки меняет выдачу; пагинация листает; оценки и обложки видны.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "feat: serve command with HTML page, filters, sorting, pagination"
```

---

### Task 10: README и финальная проверка MVP

**Files:**
- Create: `README.md`

**Interfaces:** —

- [ ] **Step 1: README**

Описать: установку, `OPENCRITIC_API_KEY`, `go run . sync` (с заметкой про время сбора и лимиты), `go run . serve`, список флагов. Упомянуть, что тесты будут добавлены после MVP.

- [ ] **Step 2: Сквозная проверка MVP**

Run: чистая БД → `OPENCRITIC_API_KEY=... go run . sync` → `go run . serve` → пройтись по фильтрам/сортировке/пагинации в браузере.
Expected: каталог PS Plus Extra TR со средними оценками отображается и интерактивен.

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "docs: README and MVP wrap-up"
```

---

## Self-Review

- **Покрытие спеки:** каталог из GraphQL (Task 2–4), оценки OpenCritic+MC (Task 5–7), SQLite-хранилище (Task 1,4,7), HTML с пагинацией/фильтрами/сортировкой (Task 8–9), env-ключ и обработка ошибок/троттлинг (Task 6–7). Тесты осознанно отложены (Global Constraints) — соответствует решению пользователя.
- **Спайки вместо выдуманных схем:** точные формы внешних API/HTML определяются в Task 2 и Task 5 и фиксируются в `docs/research/*` + `testdata/*`; парсеры (Task 3, 6) ссылаются на них. Это сознательное проектное решение, а не placeholder.
- **Согласованность типов:** `Game`/`GameRow`/`GameView`, `ListParams`, `computeAverage`, `UpdateScores`, `GamesNeedingScores`, `ListGames` определены с сигнатурами и используются согласованно между задачами.
