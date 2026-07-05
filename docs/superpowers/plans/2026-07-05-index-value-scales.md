# Индекс по шкалам значений — план реализации (этап 2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Расширить индекс быстрого перехода на сортировки по году, вердиктам критиков/игроков и времени прохождения (этап 2 спеки `2026-07-05-infinite-scroll-index-design.md`).

**Architecture:** Обобщить `LetterBucket` → `IndexBucket{Label, Offset}` и добавить диспетчер `IndexBuckets(db, p)`: буквы для `title`, `GROUP BY release_year` для `year`, декады `CAST(score/10)*10` для `critic`/`player`, пороги CASE (0–5/5–10/10–20/20–40/40–60/60+) для `hltbmain`. NULL-группа (нет значения) сортируется в конец и чип не получает; год ≤0 — чип «—» на своей позиции сортировки. Клиентский JS не меняется.

**Tech Stack:** Go + SQLite, изменений в JS нет.

## Global Constraints

- Ветка `feature/index-value-scales`; коммиты с `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.
- Пустые бакеты опускаются; при `desc` порядок и смещения разворачиваются.
- `go test ./...` перед каждым коммитом.

---

### Task 1: Переименование LetterBucket → IndexBucket (механическое, тесты остаются зелёными)

**Files:** Modify: `internal/store/query.go`, `serve.go`, `templates/index.html`, `internal/store/letters_test.go`, `serve_test.go`.

- [ ] Тип `LetterBucket{Letter,Offset}` → `IndexBucket{Label string; Offset int}`; функция `TitleLetterBuckets` остаётся (возвращает `[]IndexBucket`), `pageData.Letters` → `pageData.Buckets`; в шаблоне `.Letters`→`.Buckets`, `.Letter`→`.Label`; aria-label рейки — «Быстрый переход». Тесты правятся под новые имена.
- [ ] `go test ./...` PASS, коммит `refactor: обобщить бакеты индекса (IndexBucket)`.

### Task 2: Диспетчер IndexBuckets + бакеты по году

**Files:** Modify: `internal/store/query.go`; Test: `internal/store/letters_test.go`.

**Interfaces:** `func IndexBuckets(db *sql.DB, p ListParams) ([]IndexBucket, error)` — по `p.Sort`: `title`→буквы, `year`→годы, `critic`/`player`→декады, `hltbmain`→пороги, иначе nil. Общий walk: строки (value, count) сортируются компаратором ORDER BY (`IS NULL` в конец, значение по `p.Order`), кумулятивные смещения, NULL — без чипа.

- [ ] RED: тесты — год asc `[{«2010»,0},{«2020»,1}]`, desc наоборот; игра с годом 0 → чип «—» первым при asc; игра «без значения» не ломает смещения (для года это 0 — «—»; NULL-кейс проверяется на оценках в Task 3).
- [ ] GREEN: `IndexBuckets` + `yearBuckets` (`SELECT release_year, COUNT(*) … GROUP BY 1`), walk-хелпер `cumulate(rows, desc bool) []IndexBucket` с label-функцией.
- [ ] Коммит `feat: индекс по годам`.

### Task 3: Декады оценок critic/player

**Files:** Modify: `internal/store/query.go`; Test: `internal/store/letters_test.go`.

- [ ] RED: у игр critic_average 79.5 и 85 → asc `[{«70»,0},{«80»,1}]`, desc `[{«80»,0},{«70»,1}]`; игра без оценки (NULL) не входит и не сдвигает.
- [ ] GREEN: `SELECT CAST(<col>/10 AS INTEGER)*10, COUNT(*) … GROUP BY 1` для `critic_average_score`/`player_average_score` (колонка из белого списка, не из ввода).
- [ ] Коммит `feat: индекс по декадам оценок`.

### Task 4: Пороги времени hltbmain

**Files:** Modify: `internal/store/query.go`; Test: `internal/store/letters_test.go`.

- [ ] RED: игры 3ч и 65ч → asc `[{«0–5»,0},{«60+»,1}]`; NULL — не входит.
- [ ] GREEN: CASE по `hltb_main_extra` (секунды): <5ч→0, <10→5, <20→10, <40→20, <60→40, иначе 60; labels: 0→«0–5», 5→«5–10», 10→«10–20», 20→«20–40», 40→«40–60», 60→«60+».
- [ ] Коммит `feat: индекс по времени прохождения`.

### Task 5: Хендлер для всех сортировок + сквозная проверка

**Files:** Modify: `serve.go`; Test: `serve_test.go`.

- [ ] RED: обновить `TestHandleIndexComputesLettersOnlyForTitleSort` → `TestHandleIndexComputesBucketsPerSort`: sort=title → буквы; sort=player при наличии player_average → бакет есть; sort=average (нет в UI) → 0.
- [ ] GREEN: в `handleIndex` вместо условия `p.Sort == "title"` — всегда `store.IndexBuckets(db, p)`.
- [ ] Сквозная: живой сервер, curl — чипы годов при `sort=year`, декад при `sort=critic&order=desc` (первый чип — старшая декада), порогов при `sort=hltbmain`; смещение первого чипа 0.
- [ ] Коммит `feat: индекс быстрого перехода для всех сортировок`.
