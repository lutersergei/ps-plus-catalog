# Бесконечная лента + буквенный индекс — план реализации (этап 1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Заменить номерную пагинацию каталога бесконечной подгрузкой карточек и буквенным индексом A–Z при сортировке по названию.

**Architecture:** Сервер получает параметр `offset` (эквивалент `page`) и режим `fragment=cards`, отдающий только карточки тем же шаблонным блоком. Буквенные смещения считаются одним GROUP BY-запросом с теми же фильтрами. Клиент — ванильный JS в шаблоне: IntersectionObserver на ссылке «Показать ещё», прыжки по `data-offset`/`data-i`, `history.replaceState`.

**Tech Stack:** Go (html/template, database/sql + SQLite), ванильный JS. Без новых зависимостей.

## Global Constraints

- Спека: `docs/superpowers/specs/2026-07-05-infinite-scroll-index-design.md`.
- Ветка `feature/infinite-scroll-index`, коммиты с суффиксом `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.
- UI-копирайт на русском. Никаких JS-фреймворков и внешних зависимостей.
- Двунаправленной догрузки НЕТ: прыжок в незагруженное = перестройка ленты с этой позиции.
- Тесты запускать: `go test ./...` (без таймаут-утилиты, в окружении нет `timeout`).
- `docs/` в .gitignore — файлы плана/спеки добавлять через `git add -f`.

---

### Task 1: Сортировка по названию COLLATE NOCASE

**Files:**
- Modify: `internal/store/query.go` (map `sortColumns` ~строка 163, `orderSQL` ~строка 290)
- Test: `internal/store/letters_test.go` (создать)

**Interfaces:**
- Produces: сортировка `title` регистронезависима; вторичная сортировка `title ASC` тоже. Никаких новых экспортов.

- [ ] **Step 1: Написать падающий тест**

Создать `internal/store/letters_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
)

func TestListGamesTitleSortIsCaseInsensitive(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, g := range []GameRow{
		{ID: "g1", Title: "ALIENATION"},
		{ID: "g2", Title: "a Space for the Unbound"},
		{ID: "g3", Title: "Beta Game"},
	} {
		if err := UpsertGame(db, g); err != nil {
			t.Fatalf("upsert %s: %v", g.ID, err)
		}
	}

	res, err := ListGames(db, ListParams{Sort: "title", Order: "asc", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var got []string
	for _, g := range res.Games {
		got = append(got, g.Title)
	}
	want := []string{"a Space for the Unbound", "ALIENATION", "Beta Game"}
	if len(got) != len(want) {
		t.Fatalf("got %v, ждали %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("порядок %v, ждали %v (NOCASE)", got, want)
		}
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/store/ -run TestListGamesTitleSortIsCaseInsensitive -v`
Expected: FAIL — «a Space for the Unbound» окажется в конце (байтовое сравнение).

- [ ] **Step 3: Минимальная реализация**

В `internal/store/query.go`:

```go
var sortColumns = map[string]string{
	"year":     "release_year",
	"average":  "average_score",
	"critic":   "critic_average_score",
	"player":   "player_average_score",
	"title":    "title COLLATE NOCASE",
	"hltbmain": "hltb_main_extra",
}
```

И вторичную сортировку в `ListGames`:

```go
orderSQL := fmt.Sprintf("ORDER BY (%s IS NULL), %s %s, title COLLATE NOCASE ASC", col, col, dir)
```

- [ ] **Step 4: Тесты зелёные**

Run: `go test ./internal/store/`
Expected: PASS (все, не только новый).

- [ ] **Step 5: Commit**

```bash
git add internal/store/query.go internal/store/letters_test.go
git commit -m "fix: регистронезависимая сортировка по названию (COLLATE NOCASE)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: Буквенные бакеты TitleLetterBuckets

**Files:**
- Modify: `internal/store/query.go` (извлечь WHERE-билдер из `ListGames`, ~строки 190–266; добавить `LetterBucket`, `TitleLetterBuckets`)
- Test: `internal/store/letters_test.go`

**Interfaces:**
- Produces: `type LetterBucket struct { Letter string; Offset int }`;
  `func TitleLetterBuckets(db *sql.DB, p ListParams) ([]LetterBucket, error)` —
  бакеты в порядке выдачи списка (asc: «#», A…Z; desc: Z…A, «#»), только непустые,
  Offset = числу строк до бакета при текущих фильтрах.
- Consumes: `buildListWhere(p ListParams) (string, []any)` — извлекается здесь же.

- [ ] **Step 1: Написать падающие тесты**

Добавить в `internal/store/letters_test.go`:

```go
func TestTitleLetterBucketsCumulativeOffsets(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, g := range []GameRow{
		{ID: "g1", Title: "Alpha"},
		{ID: "g2", Title: "angel"}, // нижний регистр — тот же бакет A
		{ID: "g3", Title: "Beta"},
		{ID: "g4", Title: "42 Game"}, // не буква — бакет "#"
	} {
		if err := UpsertGame(db, g); err != nil {
			t.Fatalf("upsert %s: %v", g.ID, err)
		}
	}

	got, err := TitleLetterBuckets(db, ListParams{Sort: "title", Order: "asc"})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	want := []LetterBucket{{"#", 0}, {"A", 1}, {"B", 3}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, ждали %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, ждали %+v", got, want)
		}
	}
}

func TestTitleLetterBucketsDescReversesOrder(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, g := range []GameRow{
		{ID: "g1", Title: "Alpha"},
		{ID: "g2", Title: "Beta"},
	} {
		if err := UpsertGame(db, g); err != nil {
			t.Fatalf("upsert %s: %v", g.ID, err)
		}
	}

	got, err := TitleLetterBuckets(db, ListParams{Sort: "title", Order: "desc"})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	want := []LetterBucket{{"B", 0}, {"A", 1}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, ждали %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, ждали %+v", got, want)
		}
	}
}

func TestTitleLetterBucketsRespectsFilters(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, g := range []GameRow{
		{ID: "g1", Title: "Alpha", ReleaseYear: 2020},
		{ID: "g2", Title: "Beta", ReleaseYear: 2010},
	} {
		if err := UpsertGame(db, g); err != nil {
			t.Fatalf("upsert %s: %v", g.ID, err)
		}
	}

	got, err := TitleLetterBuckets(db, ListParams{Sort: "title", Order: "asc", YearFrom: 2015})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	want := []LetterBucket{{"A", 0}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %+v, ждали %+v (фильтр по году должен применяться)", got, want)
	}
}
```

Примечание: `GameRow` — существующий тип для `UpsertGame` (см. store_test.go);
если у него нет поля `ReleaseYear`, поставить год через
`db.Exec("UPDATE games SET release_year = ? WHERE id = ?", 2020, "g1")` по
образцу других тестов.

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `go test ./internal/store/ -run TestTitleLetterBuckets -v`
Expected: FAIL — `undefined: TitleLetterBuckets` (ошибка компиляции — валидный RED).

- [ ] **Step 3: Реализация**

В `internal/store/query.go`: извлечь построение WHERE из `ListGames` в функцию
(тело — существующие строки от `where := []string{"active = 1"}` до сборки
`whereSQL` включительно, без изменений логики):

```go
// buildListWhere собирает WHERE-условия и аргументы выборки по параметрам.
// Используется списком игр и расчётом буквенных бакетов, чтобы фильтры
// гарантированно совпадали.
func buildListWhere(p ListParams) (string, []any) {
	where := []string{"active = 1"}
	var args []any
	// ... (перенесённые без изменений блоки: поиск, год, жанры, средние,
	// критики/игроки, HLTB, языки — ровно как сейчас в ListGames)
	return "WHERE " + strings.Join(where, " AND "), args
}
```

`ListGames` вызывает `whereSQL, args := buildListWhere(p)`.

Затем бакеты:

```go
// LetterBucket — бакет буквенного индекса: буква и смещение первой строки
// с этой буквы в текущей выборке.
type LetterBucket struct {
	Letter string
	Offset int
}

// TitleLetterBuckets считает бакеты первого символа названия для буквенного
// индекса. Не-латинские первые символы попадают в бакет "#", который при
// сортировке по возрастанию идёт первым (как и в ORDER BY: цифры/символы
// раньше букв), при убывании — последним. Пустые бакеты опускаются.
func TitleLetterBuckets(db *sql.DB, p ListParams) ([]LetterBucket, error) {
	NormalizeParams(&p)
	whereSQL, args := buildListWhere(p)
	rows, err := db.Query(
		"SELECT UPPER(SUBSTR(title,1,1)), COUNT(*) FROM games "+whereSQL+" GROUP BY 1", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var l string
		var n int
		if err := rows.Scan(&l, &n); err != nil {
			return nil, err
		}
		if len(l) != 1 || l[0] < 'A' || l[0] > 'Z' {
			l = "#"
		}
		counts[l] += n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	order := []string{"#"}
	for c := byte('A'); c <= 'Z'; c++ {
		order = append(order, string(c))
	}
	if strings.EqualFold(p.Order, "desc") {
		for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
			order[i], order[j] = order[j], order[i]
		}
	}

	var buckets []LetterBucket
	offset := 0
	for _, l := range order {
		n := counts[l]
		if n == 0 {
			continue
		}
		buckets = append(buckets, LetterBucket{Letter: l, Offset: offset})
		offset += n
	}
	return buckets, nil
}
```

- [ ] **Step 4: Тесты зелёные**

Run: `go test ./internal/store/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/query.go internal/store/letters_test.go
git commit -m "feat: буквенные бакеты для индекса каталога

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: Параметр offset в хендлере

**Files:**
- Modify: `serve.go` (`handleIndex`, парсинг параметров ~строка 127)
- Test: `serve_test.go`

**Interfaces:**
- Produces: GET-параметр `offset` (строки выдачи); при заданном `offset ≥ 0`
  он переопределяет `page`: `p.Page = offset/pageSize + 1` (округление вниз до
  границы страницы — осознанное упрощение, JS докручивает до точной карточки).

- [ ] **Step 1: Падающий тест**

В `serve_test.go`:

```go
func TestHandleIndexOffsetParamOverridesPage(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// 30 игр: G01..G30 — при pageSize 24 offset=24 начинает с G25.
	for i := 1; i <= 30; i++ {
		id := fmt.Sprintf("g%02d", i)
		title := fmt.Sprintf("G%02d", i)
		if err := store.UpsertGame(db, store.GameRow{ID: id, Title: title}); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	tmpl := template.Must(template.New("test").Parse(
		`first={{(index .Result.Games 0).Title}} page={{.Result.Page}}`))
	req := httptest.NewRequest("GET", "/?offset=24&page=1&sort=title&order=asc", nil)
	rec := httptest.NewRecorder()

	handleIndex(rec, req, db, tmpl)

	body := rec.Body.String()
	if !strings.Contains(body, "first=G25") || !strings.Contains(body, "page=2") {
		t.Fatalf("body=%q, ждали first=G25 page=2 (offset приоритетнее page)", body)
	}
}
```

Добавить `"fmt"` в импорты serve_test.go.

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test . -run TestHandleIndexOffsetParamOverridesPage -v`
Expected: FAIL — offset игнорируется, first=G01 page=1.

- [ ] **Step 3: Реализация**

В `handleIndex` сразу после построения `p := store.ListParams{...}`:

```go
	// offset — альтернатива page для бесконечной ленты: число строк от начала
	// выдачи. Округляется вниз до границы страницы; точную карточку внутри
	// партии докручивает клиентский JS.
	if off := atoiDefault(q.Get("offset"), -1); off >= 0 {
		p.Page = off/pageSize + 1
	}
```

- [ ] **Step 4: Тесты зелёные**

Run: `go test .`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add serve.go serve_test.go
git commit -m "feat: параметр offset как альтернатива page

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: Режим fragment=cards и data-i на карточках

**Files:**
- Modify: `templates/index.html` (обернуть карточки в `{{define "cards"}}`, добавить `data-i`)
- Modify: `serve.go` (ветка fragment в `handleIndex`, функция `mul` в FuncMap)
- Test: `serve_test.go`

**Interfaces:**
- Produces: `GET /?fragment=cards&offset=N&<фильтры>` → HTML только карточек
  (`div.gcard` с `data-i` = глобальный порядковый номер), заголовок `X-Total`.
  Шаблонный блок `cards` рендерится и полной страницей — вёрстка одна.
- Consumes: `offset` из Task 3.

- [ ] **Step 1: Падающие тесты**

В `serve_test.go`:

```go
func TestFragmentRendersOnlyCardsWithTotalHeader(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	for i := 1; i <= 30; i++ {
		id := fmt.Sprintf("g%02d", i)
		if err := store.UpsertGame(db, store.GameRow{ID: id, Title: fmt.Sprintf("G%02d", i)}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	tmpl, err := newIndexTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req := httptest.NewRequest("GET", "/?fragment=cards&offset=24&sort=title&order=asc", nil)
	rec := httptest.NewRecorder()

	handleIndex(rec, req, db, tmpl)

	body := rec.Body.String()
	if !strings.Contains(body, `class="gcard"`) || strings.Contains(body, "<form") || strings.Contains(body, "<head>") {
		t.Fatalf("фрагмент должен содержать только карточки, body[:200]=%q", body[:min(200, len(body))])
	}
	if got := rec.Header().Get("X-Total"); got != "30" {
		t.Fatalf("X-Total=%q, ждали 30", got)
	}
	// вторая партия начинается с глобального номера 24
	if !strings.Contains(body, `data-i="24"`) {
		t.Fatalf("нет data-i=24 в теле фрагмента")
	}
}

func TestFullPageCardsCarryDataIndex(t *testing.T) {
	tmpl, err := newIndexTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := pageData{
		Result: store.ListResult{
			Games:    []store.GameView{{ID: "g1", Title: "Game"}},
			Page:     3,
			PageSize: 24,
		},
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), `data-i="48"`) {
		t.Fatalf("карточка на странице 3 должна иметь data-i=48")
	}
}
```

(`min` доступен в Go 1.21+; модуль его поддерживает — см. go.mod.)

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `go test . -run 'TestFragment|TestFullPageCards' -v`
Expected: FAIL — нет блока cards/data-i, фрагмент отдаёт полную страницу.

- [ ] **Step 3: Реализация**

`serve.go` — в FuncMap добавить `"mul": func(a, b int) int { return a * b },`.

В `handleIndex` сразу после успешного `store.ListGames` (до запросов Years/Genres):

```go
	// Режим фрагмента: только карточки для бесконечной ленты. Общие число —
	// в заголовке, чтобы клиент обновлял счётчик без парсинга HTML.
	if q.Get("fragment") == "cards" {
		w.Header().Set("X-Total", strconv.Itoa(result.Total))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.ExecuteTemplate(w, "cards", pageData{Result: result}); err != nil {
			log.Printf("render fragment: %v", err)
		}
		return
	}
```

`templates/index.html` — обернуть карточки. Вместо текущего:

```
{{range .Result.Games}}
  <div class="gcard">
```

сделать (закрывающий `{{end}}` цикла остаётся на месте, после него добавить `{{end}}` define; вызов — на месте старого цикла):

```
{{define "cards"}}
{{$base := mul (add .Result.Page -1) .Result.PageSize}}
{{range $idx, $g := .Result.Games}}
  <div class="gcard" data-i="{{add $base $idx}}">
  ... (тело карточки БЕЗ изменений: внутри range точка остаётся элементом)
  </div>
{{end}}
{{end}}
```

а в полной странице внутри `<div class="grid">`: `{{template "cards" .}}`.

Важно: `{{define}}` должен стоять вне `<div class="grid">` (в конце файла или
до разметки), а в гриде — только `{{template "cards" .}}`.

Примечание для тестового рендера: в `TestFragmentRendersOnlyCardsWithTotalHeader`
тело — фрагмент, поэтому «нет `<head>`» проверяет, что рендерится именно блок.

- [ ] **Step 4: Тесты зелёные**

Run: `go test .`
Expected: PASS (включая старые шаблонные тесты).

- [ ] **Step 5: Commit**

```bash
git add serve.go templates/index.html serve_test.go
git commit -m "feat: фрагмент карточек fragment=cards и data-i

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: Страница — буквенный индекс, «Показать ещё», счётчик; удаление пагинации

**Files:**
- Modify: `serve.go` (`pageData`: поля `Letters`, `NextOffset`; убрать `Pages`, `HasPrev`, `pageWindow`; заполнение в `handleIndex`)
- Modify: `templates/index.html` (рейка индекса, ссылка-fallback, счётчик; удалить `.pager`-разметку и CSS)
- Test: `serve_test.go` (заменить `TestIndexTemplateRendersPagerWindow`)

**Interfaces:**
- Consumes: `store.TitleLetterBuckets` (Task 2), `mul` (Task 4).
- Produces: `pageData.Letters []store.LetterBucket`, `pageData.NextOffset int`
  (= `Result.Page*Result.PageSize`); разметка `a.achip[data-offset]`,
  `a#moreLink`, `#shownCount`, `.grid[data-next][data-psize]`.

- [ ] **Step 1: Падающие тесты**

Удалить `TestIndexTemplateRendersPagerWindow` целиком. Добавить:

```go
func TestIndexTemplateRendersLetterIndexAndMoreLink(t *testing.T) {
	tmpl, err := newIndexTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := pageData{
		Result: store.ListResult{
			Games:      []store.GameView{{ID: "g1", Title: "Game"}},
			Total:      469,
			Page:       1,
			PageSize:   24,
			TotalPages: 20,
		},
		Params:     store.ListParams{Sort: "title"},
		BaseQuery:  template.URL("sort=title&order=asc"),
		Letters:    []store.LetterBucket{{Letter: "#", Offset: 0}, {Letter: "A", Offset: 3}},
		NextOffset: 24,
		HasNext:    true,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`class="achip" data-offset="3"`,
		`id="moreLink"`,
		`offset=24`,
		`Показать ещё`,
		`id="shownCount"`,
		`data-next="24"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered template missing %q", want)
		}
	}
	if strings.Contains(body, `class="pager"`) {
		t.Fatalf("номерная пагинация должна быть удалена")
	}
}

func TestIndexTemplateHidesLetterIndexWithoutBuckets(t *testing.T) {
	tmpl, err := newIndexTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := pageData{
		Result: store.ListResult{Games: []store.GameView{{ID: "g1", Title: "Game"}}},
		Params: store.ListParams{Sort: "player"},
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(buf.String(), "achip") {
		t.Fatalf("индекс не должен рендериться без бакетов")
	}
}

func TestHandleIndexComputesLettersOnlyForTitleSort(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := store.UpsertGame(db, store.GameRow{ID: "g1", Title: "Alpha"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	tmpl := template.Must(template.New("test").Parse(`letters={{len .Letters}}`))

	rec := httptest.NewRecorder()
	handleIndex(rec, httptest.NewRequest("GET", "/?sort=title", nil), db, tmpl)
	if !strings.Contains(rec.Body.String(), "letters=1") {
		t.Fatalf("body=%q, ждали letters=1 при sort=title", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handleIndex(rec, httptest.NewRequest("GET", "/?sort=player", nil), db, tmpl)
	if !strings.Contains(rec.Body.String(), "letters=0") {
		t.Fatalf("body=%q, ждали letters=0 при sort=player", rec.Body.String())
	}
}
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `go test . -run 'LetterIndex|HidesLetter|ComputesLetters' -v`
Expected: FAIL — нет полей Letters/NextOffset (ошибка компиляции — валидный RED).

- [ ] **Step 3: Реализация**

`serve.go`:

```go
type pageData struct {
	Result     store.ListResult
	Years      []int
	Genres     []string
	Params     store.ListParams
	BaseQuery  template.URL // query без page/offset — для ссылок ленты и индекса
	Letters    []store.LetterBucket
	NextOffset int // смещение следующей партии для «Показать ещё»
	HasNext    bool
}
```

Удалить функцию `pageWindow` и её использование. В `handleIndex` после
получения `result` (в полной ветке, не во фрагменте):

```go
	var letters []store.LetterBucket
	if p.Sort == "title" {
		letters, err = store.TitleLetterBuckets(db, p)
		if err != nil {
			log.Printf("letter buckets: %v", err)
			http.Error(w, "внутренняя ошибка сервера", http.StatusInternalServerError)
			return
		}
	}
```

и в `data := pageData{...}`: `Letters: letters, NextOffset: result.Page * result.PageSize, HasNext: result.Page < result.TotalPages` (поля Pages/HasPrev убрать).

`templates/index.html`:

1. Обернуть сетку в контейнер с рейкой (внутри `{{if .Result.Games}}`):

```html
<div class="catalog">
  <div class="grid" data-next="{{.NextOffset}}" data-psize="{{.Result.PageSize}}">
    {{template "cards" .}}
  </div>
  {{if .Letters}}
  <nav class="alpha" aria-label="Быстрый переход по алфавиту">
    {{$bq := .BaseQuery}}
    {{range .Letters}}<a class="achip" data-offset="{{.Offset}}" href="?{{$bq}}&offset={{.Offset}}">{{.Letter}}</a>{{end}}
  </nav>
  {{end}}
</div>
```

2. Вместо `<nav class="pager">…</nav>` и старого `.pcount`:

```html
{{if .HasNext}}
<div class="morewrap">
  <a class="btn more" id="moreLink" href="?{{.BaseQuery}}&offset={{.NextOffset}}">Показать ещё</a>
</div>
{{end}}
<div class="pcount">Показано <b id="shownCount">{{len .Result.Games}}</b> из <b id="totalCount">{{.Result.Total}}</b> игр</div>
```

3. CSS: удалить блок `.pager …` (правила `.pager`, `.pager a`, `.cur`, `.dis`, `.gap`), добавить:

```css
.catalog { display: flex; gap: 14px; align-items: flex-start; padding: 22px 0; }
.catalog .grid { flex: 1; padding: 0; }
.alpha {
  position: sticky; top: 12px;
  display: flex; flex-direction: column; gap: 3px;
  max-height: calc(100vh - 24px); overflow-y: auto;
}
.achip {
  min-width: 26px; text-align: center;
  padding: 4px 6px;
  border: 1px solid var(--line-soft); border-radius: 6px;
  background: var(--card);
  font: 700 11px var(--mono);
  color: var(--muted); text-decoration: none;
}
.achip:hover { color: var(--cobalt); border-color: var(--cobalt); }
.achip.on { background: var(--cobalt); border-color: var(--cobalt); color: var(--paper); }
.morewrap { display: flex; justify-content: center; padding: 4px 0 14px; }
a.more { text-decoration: none; }
@media (max-width: 1200px) {
  .catalog { display: block; }
  .alpha {
    position: static; flex-direction: row;
    overflow-x: auto; max-height: none;
    margin-top: 14px; padding-bottom: 4px;
  }
}
```

(грид в `.catalog` теряет свой вертикальный padding — он переехал на контейнер.)

- [ ] **Step 4: Тесты зелёные**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add serve.go templates/index.html serve_test.go
git commit -m "feat: буквенный индекс и ссылка «Показать ещё» вместо пагинации

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 6: Клиентский JS — подгрузка, прыжки, URL

**Files:**
- Modify: `templates/index.html` (inline `<script>` в конце)

**Interfaces:**
- Consumes: `#moreLink`, `.grid[data-next][data-psize]`, `a.achip[data-offset]`,
  `#shownCount`, `data-i` карточек, `fragment=cards` + `X-Total`.

- [ ] **Step 1: Добавить JS**

В конец существующего `<script>` шаблона:

```js
/* Бесконечная лента и буквенный индекс */
(function () {
  var grid = document.querySelector('.grid');
  var more = document.getElementById('moreLink');
  var shown = document.getElementById('shownCount');
  if (!grid) return;
  var psize = +grid.dataset.psize || 24;
  var inflight = false;
  var smooth = matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth';

  function setOffsetParam(url, off) {
    return url.replace(/([?&])offset=\d+/, '$1offset=' + off);
  }
  function updateCount() {
    if (shown) shown.textContent = grid.children.length;
  }
  function fetchCards(off) {
    var base = more ? more.href : (location.pathname + location.search);
    var url = /[?&]offset=/.test(base) ? setOffsetParam(base, off) : base + (base.indexOf('?') < 0 ? '?' : '&') + 'offset=' + off;
    return fetch(url + '&fragment=cards').then(function (r) {
      if (!r.ok) throw new Error(r.status);
      return r.text();
    }).then(function (html) {
      var t = document.createElement('template');
      t.innerHTML = html;
      var cards = t.content.querySelectorAll('.gcard');
      return cards;
    });
  }
  function appendCards(cards) {
    for (var i = 0; i < cards.length; i++) grid.appendChild(cards[i]);
    updateCount();
  }
  function syncURL(off) {
    var u = new URL(location.href);
    u.searchParams.set('offset', off);
    history.replaceState(null, '', u);
  }
  function endOfList() {
    if (more) { more.parentNode.removeChild(more); more = null; }
  }

  /* Подгрузка следующей партии, когда «Показать ещё» въезжает в вьюпорт */
  function loadNext() {
    if (inflight || !more) return;
    inflight = true;
    var next = +grid.dataset.next;
    fetchCards(next).then(function (cards) {
      appendCards(cards);
      grid.dataset.next = next + cards.length;
      if (cards.length < psize) { endOfList(); }
      else { more.href = setOffsetParam(more.href, next + cards.length); }
      syncURL(next);
      inflight = false;
    }).catch(function () { inflight = false; }); // ссылка остаётся — ручной retry
  }
  if (more) {
    new IntersectionObserver(function (es) {
      if (es[0].isIntersecting) loadNext();
    }, { rootMargin: '400px' }).observe(more);
    more.addEventListener('click', function (e) { e.preventDefault(); loadNext(); });
  }

  /* Прыжок по букве: скролл, если карточка в DOM; иначе перестройка ленты */
  var alpha = document.querySelector('.alpha');
  if (alpha) alpha.addEventListener('click', function (e) {
    var chip = e.target.closest('.achip');
    if (!chip) return;
    e.preventDefault();
    var off = +chip.dataset.offset;
    var target = grid.querySelector('[data-i="' + off + '"]');
    if (target) {
      target.scrollIntoView({ behavior: smooth, block: 'start' });
      syncURL(off - off % psize);
      return;
    }
    if (inflight) return;
    inflight = true;
    var aligned = off - off % psize;
    fetchCards(aligned).then(function (cards) {
      grid.innerHTML = '';
      appendCards(cards);
      grid.dataset.next = aligned + cards.length;
      if (more) {
        if (cards.length < psize) endOfList();
        else more.href = setOffsetParam(more.href, aligned + cards.length);
      }
      syncURL(aligned);
      var t2 = grid.querySelector('[data-i="' + off + '"]');
      if (t2) t2.scrollIntoView({ behavior: smooth, block: 'start' });
      inflight = false;
    }).catch(function () { inflight = false; });
  });

  /* Подсветка активной буквы по первой видимой карточке */
  var chips = alpha ? Array.prototype.slice.call(alpha.querySelectorAll('.achip')) : [];
  function markActive() {
    if (!chips.length) return;
    var cards = grid.children;
    var topCard = null;
    for (var i = 0; i < cards.length; i++) {
      if (cards[i].getBoundingClientRect().bottom > 0) { topCard = cards[i]; break; }
    }
    if (!topCard) return;
    var idx = +topCard.dataset.i;
    var active = null;
    for (var j = 0; j < chips.length; j++) {
      if (+chips[j].dataset.offset <= idx) active = chips[j];
    }
    chips.forEach(function (c) { c.classList.toggle('on', c === active); });
  }
  var ticking = false;
  addEventListener('scroll', function () {
    if (ticking) return;
    ticking = true;
    requestAnimationFrame(function () { markActive(); ticking = false; });
  }, { passive: true });
  markActive();
})();
```

- [ ] **Step 2: Smoke-проверка рендера**

Run: `go test ./...`
Expected: PASS (шаблон парсится, старые тесты живы).

- [ ] **Step 3: Commit**

```bash
git add templates/index.html
git commit -m "feat: клиентская бесконечная подгрузка и прыжки по индексу

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 7: Сквозная верификация

**Files:** нет новых (фиксы — по месту).

- [ ] **Step 1: Полный прогон**

Run: `gofmt -l . | grep -v vendor; go vet ./...; go test ./...`
Expected: пусто у gofmt, PASS у тестов.

- [ ] **Step 2: Живой сервер**

```bash
go build -o /tmp-scratchpad/ps-extra-bin . && /tmp-scratchpad/ps-extra-bin serve -db ps-extra.db -addr 127.0.0.1:8199 &
sleep 1
curl -s --noproxy '*' 'http://127.0.0.1:8199/?sort=title&order=asc' | grep -c 'achip'          # >0 букв
curl -s --noproxy '*' 'http://127.0.0.1:8199/?sort=title&order=asc' | grep -o 'id="moreLink"'  # есть ссылка
curl -s --noproxy '*' 'http://127.0.0.1:8199/?sort=player&order=desc' | grep -c 'achip'        # 0
curl -sI --noproxy '*' 'http://127.0.0.1:8199/?fragment=cards&offset=24' | grep 'X-Total'
curl -s --noproxy '*' 'http://127.0.0.1:8199/?fragment=cards&offset=24' | grep -o 'data-i="24"'
curl -s --noproxy '*' 'http://127.0.0.1:8199/?sort=title' | grep -o 'a Space[^<]*'             # NOCASE не в конце: сверить соседей
kill %1
```

Expected: буквы есть только при sort=title; фрагмент — карточки с data-i и X-Total.

- [ ] **Step 3: Коммит фиксов (если были)**

```bash
git add -A -- ':!docs' ':!.DS_Store' && git commit -m "fix: правки по итогам сквозной проверки

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Self-review плана

- Покрытие спеки: offset (T3), fragment+X-Total (T4), бакеты+NOCASE+«#» (T1–T2),
  индекс/ссылка/счётчик/удаление пагинации (T5), JS-лента/прыжки/URL/подсветка/
  reduced-motion/ретрай (T6), верификация (T7). Этап 2 спеки — вне плана,
  осознанно.
- Типы согласованы: `LetterBucket{Letter,Offset}` (T2) = `pageData.Letters` (T5)
  = `data-offset` (T5/T6); `NextOffset` (T5) = `data-next` (T6).
- Плейсхолдеров нет; единственная отсылка «перенесённые блоки без изменений» в
  buildListWhere — это перенос существующего кода, не новый.
