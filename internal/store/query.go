package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// ListParams — параметры выборки игр для страницы.
type ListParams struct {
	Search        string   // поиск по названию (подстрока; пусто = все)
	Genres        []string // фильтр по жанрам (OR; пусто = все)
	YearFrom      int      // нижняя граница года выпуска (0 = не задана)
	YearTo        int      // верхняя граница года выпуска (0 = не задана)
	AvgFrom       float64  // нижняя граница среднего рейтинга (0 = не задана)
	AvgTo         float64  // верхняя граница среднего рейтинга (0 = не задана)
	CriticFrom    float64  // нижняя граница оценки критиков (0 = не задана)
	CriticTo      float64  // верхняя граница оценки критиков (0 = не задана)
	PlayerFrom    float64  // нижняя граница оценки игроков (0 = не задана)
	PlayerTo      float64  // верхняя граница оценки игроков (0 = не задана)
	ReviewsFrom   int      // нижняя граница суммы пользовательских оценок MC+OC (0 = не задана)
	ReviewsTo     int      // верхняя граница суммы пользовательских оценок MC+OC (0 = не задана)
	HLTBFromHours float64  // нижняя граница Main+Sides в часах (0 = не задана)
	HLTBToHours   float64  // верхняя граница Main+Sides в часах (0 = не задана)
	Sort          string   // "year" | "average" | "critic" | "player" | "title" | "hltbmain" | "reviews" | "added"
	Order         string   // "asc" | "desc"
	Page          int      // с 1
	PageSize      int
	RuSubtitles   bool // только игры с русскими субтитрами/интерфейсом
	RuVoice       bool // только игры с русской озвучкой
}

// GameView — игра для отображения.
type GameView struct {
	ID                    string
	Title                 string
	TitleEn               string
	ReleaseYear           int
	Genres                []string
	Platforms             string
	ImageURL              string
	StoreURL              string
	Metacritic            sql.NullInt64
	MetacriticPageURL     sql.NullString
	MetacriticUser        sql.NullInt64
	MetacriticUserCount   sql.NullInt64
	OpenCritic            sql.NullInt64
	OpenCriticPlayer      sql.NullInt64
	OpenCriticPlayerCount sql.NullInt64
	OpenCriticPageURL     sql.NullString
	Average               sql.NullFloat64
	CriticAverage         sql.NullFloat64
	PlayerAverage         sql.NullFloat64
	HLTBMainSec           sql.NullInt64 // Main + Sides, секунды
	HLTBRating            sql.NullInt64 // рейтинг HLTB (0–100)
	HLTBPageURL           sql.NullString
	CatalogAddedOn        sql.NullTime
	CatalogAddedSource    sql.NullString
	CatalogSourceURL      sql.NullString
	RuSub                 bool // есть русские субтитры/интерфейс
	RuVoice               bool // есть русская озвучка
}

// HLTBHours возвращает Main+Sides в часах (для шаблона), 0 если нет данных.
func (g GameView) HLTBHours() float64 {
	if !g.HLTBMainSec.Valid {
		return 0
	}
	return float64(g.HLTBMainSec.Int64) / 3600
}

// CatalogAddedLabel форматирует календарную дату добавления для карточки.
func (g GameView) CatalogAddedLabel() string {
	if !g.CatalogAddedOn.Valid {
		return ""
	}
	return g.CatalogAddedOn.Time.Format("02.01.2006")
}

// CatalogAddedTitle объясняет точность даты: официальный анонс даёт точный
// день, observed означает первый успешный снимок, в котором замечена игра.
func (g GameView) CatalogAddedTitle() string {
	if !g.CatalogAddedOn.Valid {
		return ""
	}
	if g.CatalogAddedSource.Valid && g.CatalogAddedSource.String == "announcement" {
		return "Дата добавления по официальному анонсу PlayStation"
	}
	return "Дата первого наблюдения в каталоге"
}

// RuStoreURL — ссылка на страницу игры в русском PS Store. Каталог собирается
// из турецкого региона, но пользователю удобнее русская страница магазина —
// подменяем локаль только при отображении, данные остаются каноничными.
func (g GameView) RuStoreURL() string {
	return strings.Replace(g.StoreURL, "/tr-tr/", "/ru-ru/", 1)
}

// OCPlayerWeight — вес пользовательской оценки OpenCritic в среднем игроков.
// Зеркалирует SQL-выражение openCriticPlayerWeightExpr (games.go): без оценки
// или при <20 голосов — 0, при >100 голосов — 1, иначе 0.5.
func (g GameView) OCPlayerWeight() float64 {
	if !g.OpenCriticPlayer.Valid || g.OpenCriticPlayer.Int64 <= 0 {
		return 0
	}
	count := int64(0)
	if g.OpenCriticPlayerCount.Valid {
		count = g.OpenCriticPlayerCount.Int64
	}
	switch {
	case count < 20:
		return 0
	case count > 100:
		return 1
	default:
		return 0.5
	}
}

// OCWeightGlyph — глиф веса пользовательской оценки OpenCritic для шаблона:
// ● полный вес, ◐ половинный, ○ не учтена. Пустая строка, когда данных
// OpenCritic об оценке игроков нет вовсе (нечего объяснять).
func (g GameView) OCWeightGlyph() string {
	if !g.OpenCriticPlayer.Valid && !g.OpenCriticPlayerCount.Valid {
		return ""
	}
	switch g.OCPlayerWeight() {
	case 1:
		return "●"
	case 0.5:
		return "◐"
	default:
		return "○"
	}
}

var termCleaner = strings.NewReplacer("™", "", "®", "", "’", "'")

// searchTerm — название для поиска на внешних ресурсах (английское, иначе
// локализованное), без символов ™®, мешающих поиску.
func (g GameView) searchTerm() string {
	t := g.TitleEn
	if t == "" {
		t = g.Title
	}
	return strings.TrimSpace(termCleaner.Replace(t))
}

// MetacriticURL возвращает сохранённую страницу игры. Без неё нельзя надёжно
// угадать, какой slug прошёл при сборе оценки, поэтому используем поиск.
func (g GameView) MetacriticURL() string {
	if g.MetacriticPageURL.Valid && strings.HasPrefix(g.MetacriticPageURL.String, "https://www.metacritic.com/") {
		return g.MetacriticPageURL.String
	}
	return "https://www.metacritic.com/search/" + url.PathEscape(g.searchTerm()) + "/"
}

// OpenCriticURL и HLTBURL ведут на прямую страницу, если при синке сохранён
// канонический URL. Иначе остаётся search fallback.

func (g GameView) OpenCriticURL() string {
	if g.OpenCriticPageURL.Valid && strings.HasPrefix(g.OpenCriticPageURL.String, "https://opencritic.com/") {
		return g.OpenCriticPageURL.String
	}
	return "https://opencritic.com/search?term=" + url.QueryEscape(g.searchTerm())
}

func (g GameView) HLTBURL() string {
	if g.HLTBPageURL.Valid && strings.HasPrefix(g.HLTBPageURL.String, "https://howlongtobeat.com/") {
		return g.HLTBPageURL.String
	}
	return "https://howlongtobeat.com/?q=" + url.QueryEscape(g.searchTerm())
}

// ListResult — страница результатов с метаданными пагинации.
type ListResult struct {
	Games      []GameView
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

// sortColumns — белый список колонок сортировки (защита от SQL-инъекции).
var sortColumns = map[string]string{
	"year":     "release_year",
	"average":  "average_score",
	"critic":   "critic_average_score",
	"player":   "player_average_score",
	"title":    "title COLLATE NOCASE",
	"hltbmain": "hltb_main_extra",
	"reviews":  reviewCountExpr,
	"added":    currentCatalogAddedExpr,
}

const reviewCountExpr = "(COALESCE(metacritic_user_count, 0) + COALESCE(opencritic_player_count, 0))"

const (
	currentCatalogAddedExpr = `(SELECT cm.added_on FROM catalog_memberships cm
		WHERE cm.game_id = games.id AND cm.removed_on IS NULL LIMIT 1)`
	currentCatalogAddedSourceExpr = `(SELECT cm.added_on_source FROM catalog_memberships cm
		WHERE cm.game_id = games.id AND cm.removed_on IS NULL LIMIT 1)`
	currentCatalogSourceURLExpr = `(SELECT cm.source_url FROM catalog_memberships cm
		WHERE cm.game_id = games.id AND cm.removed_on IS NULL LIMIT 1)`
)

// Границы пользовательских параметров: защита от чрезмерных значений из query
// string (раздутый SQL, переполнение OFFSET и т.п.).
const (
	maxSearchLen = 200 // символов в строке поиска
	maxGenres    = 50  // значений genre за запрос
)

// likeEscape экранирует спецсимволы LIKE (% _ \) во вводе пользователя.
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// NormalizeParams приводит параметры выборки к безопасным диапазонам: нулевая/
// отрицательная страница и размер, чрезмерная длина поиска, слишком длинный список
// жанров и перевёрнутые диапазоны отсекаются. Вызывающий код (HTTP-хендлер) должен
// вызвать её ДО построения формы и ссылок пагинации, чтобы отображение и ссылки
// совпадали с тем, что реально ушло в SQL. ListGames вызывает её повторно
// (идемпотентно). Верхний клампинг номера страницы — отдельно в ListGames, после
// подсчёта общего числа страниц (нужен Total).
func NormalizeParams(p *ListParams) {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = 24
	}
	if r := []rune(p.Search); len(r) > maxSearchLen {
		p.Search = string(r[:maxSearchLen]) // по рунам: не резать UTF-8 посередине
	}
	if len(p.Genres) > maxGenres {
		p.Genres = p.Genres[:maxGenres]
	}
	// Границы-диапазоны: верхняя не может быть меньше нижней — игнорируем такую пару.
	if p.YearFrom > 0 && p.YearTo > 0 && p.YearTo < p.YearFrom {
		p.YearFrom, p.YearTo = 0, 0
	}
	if p.AvgFrom > 0 && p.AvgTo > 0 && p.AvgTo < p.AvgFrom {
		p.AvgFrom, p.AvgTo = 0, 0
	}
	if p.CriticFrom > 0 && p.CriticTo > 0 && p.CriticTo < p.CriticFrom {
		p.CriticFrom, p.CriticTo = 0, 0
	}
	if p.PlayerFrom > 0 && p.PlayerTo > 0 && p.PlayerTo < p.PlayerFrom {
		p.PlayerFrom, p.PlayerTo = 0, 0
	}
	if p.ReviewsFrom > 0 && p.ReviewsTo > 0 && p.ReviewsTo < p.ReviewsFrom {
		p.ReviewsFrom, p.ReviewsTo = 0, 0
	}
	if p.HLTBFromHours > 0 && p.HLTBToHours > 0 && p.HLTBToHours < p.HLTBFromHours {
		p.HLTBFromHours, p.HLTBToHours = 0, 0
	}
}

// buildListWhere собирает WHERE-условия и аргументы выборки по параметрам.
// Используется списком игр и расчётом буквенных бакетов, чтобы фильтры
// гарантированно совпадали.
func buildListWhere(p ListParams) (string, []any) {
	where := []string{"active = 1"}
	var args []any

	// Поиск по названию (подстрока в локализованном и английском названии)
	if s := strings.TrimSpace(p.Search); s != "" {
		like := "%" + likeEscape(s) + "%"
		where = append(where, `(title LIKE ? ESCAPE '\' OR COALESCE(title_en,'') LIKE ? ESCAPE '\')`)
		args = append(args, like, like)
	}

	// Фильтр по году: диапазон
	if p.YearFrom > 0 {
		where = append(where, "release_year >= ?")
		args = append(args, p.YearFrom)
	}
	if p.YearTo > 0 {
		where = append(where, "release_year <= ?")
		args = append(args, p.YearTo)
	}

	// Фильтр по жанрам: мультивыбор (OR — хотя бы один из выбранных)
	if len(p.Genres) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(p.Genres)), ",")
		where = append(where, "id IN (SELECT game_id FROM game_genres WHERE genre IN ("+placeholders+"))")
		for _, g := range p.Genres {
			args = append(args, g)
		}
	}

	// Фильтр по среднему рейтингу
	if p.AvgFrom > 0 {
		where = append(where, "average_score >= ?")
		args = append(args, p.AvgFrom)
	}
	if p.AvgTo > 0 {
		where = append(where, "average_score <= ?")
		args = append(args, p.AvgTo)
	}
	if p.CriticFrom > 0 {
		where = append(where, "critic_average_score >= ?")
		args = append(args, p.CriticFrom)
	}
	if p.CriticTo > 0 {
		where = append(where, "critic_average_score <= ?")
		args = append(args, p.CriticTo)
	}
	if p.PlayerFrom > 0 {
		where = append(where, "player_average_score >= ?")
		args = append(args, p.PlayerFrom)
	}
	if p.PlayerTo > 0 {
		where = append(where, "player_average_score <= ?")
		args = append(args, p.PlayerTo)
	}
	if p.ReviewsFrom > 0 {
		where = append(where, reviewCountExpr+" >= ?")
		args = append(args, p.ReviewsFrom)
	}
	if p.ReviewsTo > 0 {
		where = append(where, reviewCountExpr+" <= ?")
		args = append(args, p.ReviewsTo)
	}

	// Фильтр по времени Main+Sides (в часах → секунды в БД)
	if p.HLTBFromHours > 0 {
		where = append(where, "hltb_main_extra >= ?")
		args = append(args, p.HLTBFromHours*3600)
	}
	if p.HLTBToHours > 0 {
		where = append(where, "hltb_main_extra <= ?")
		args = append(args, p.HLTBToHours*3600)
	}

	// Фильтр по языку: ищем код "ru" в JSON-массиве (безопасно — двухбуквенный код в кавычках)
	if p.RuSubtitles {
		where = append(where, `screen_langs LIKE '%"ru"%'`)
	}
	if p.RuVoice {
		where = append(where, `spoken_langs LIKE '%"ru"%'`)
	}

	return "WHERE " + strings.Join(where, " AND "), args
}

// IndexBucket — бакет индекса быстрого перехода: подпись чипа и смещение
// первой строки бакета в текущей выборке.
type IndexBucket struct {
	Label  string
	Offset int
}

// IndexBuckets возвращает бакеты индекса быстрого перехода для активной
// сортировки: буквы для title, годы для year, декады оценок для critic/player,
// пороги часов для hltbmain. Для прочих сортировок индекс не строится (nil).
func IndexBuckets(db *sql.DB, p ListParams) ([]IndexBucket, error) {
	switch p.Sort {
	case "title":
		return TitleIndexBuckets(db, p)
	case "year":
		return valueIndexBuckets(db, p, "release_year", yearBucketLabel)
	case "critic":
		return valueIndexBuckets(db, p, decadeExpr("critic_average_score"), decadeBucketLabel)
	case "player":
		return valueIndexBuckets(db, p, decadeExpr("player_average_score"), decadeBucketLabel)
	case "hltbmain":
		return valueIndexBuckets(db, p, hltbThresholdExpr, hltbBucketLabel)
	case "reviews":
		return valueIndexBuckets(db, p, reviewCountThresholdExpr, reviewCountBucketLabel)
	case "added":
		return AddedIndexBuckets(db, p)
	default:
		return nil, nil
	}
}

const reviewCountThresholdExpr = `CASE
  WHEN ` + reviewCountExpr + ` = 0 THEN 0
  WHEN ` + reviewCountExpr + ` < 100 THEN 1
  WHEN ` + reviewCountExpr + ` < 500 THEN 100
  WHEN ` + reviewCountExpr + ` < 1000 THEN 500
  WHEN ` + reviewCountExpr + ` < 5000 THEN 1000
  ELSE 5000 END`

func reviewCountBucketLabel(v int64) string {
	switch v {
	case 0:
		return "0"
	case 1:
		return "1–99"
	case 100:
		return "100–499"
	case 500:
		return "500–999"
	case 1000:
		return "1к–4.9к"
	default:
		return "5к+"
	}
}

// hltbThresholdExpr — нижняя граница диапазона времени прохождения в часах.
// Пороги неравномерные: коротких игр в каталоге больше, поэтому шаг в начале
// шкалы мельче (0/5/10/20/40/60+).
const hltbThresholdExpr = `CASE
  WHEN hltb_main_extra IS NULL THEN NULL
  WHEN hltb_main_extra < 5*3600 THEN 0
  WHEN hltb_main_extra < 10*3600 THEN 5
  WHEN hltb_main_extra < 20*3600 THEN 10
  WHEN hltb_main_extra < 40*3600 THEN 20
  WHEN hltb_main_extra < 60*3600 THEN 40
  ELSE 60 END`

// hltbBucketLabel — подпись диапазона времени по его нижней границе.
func hltbBucketLabel(v int64) string {
	switch v {
	case 0:
		return "0–5"
	case 5:
		return "5–10"
	case 10:
		return "10–20"
	case 20:
		return "20–40"
	case 40:
		return "40–60"
	default:
		return "60+"
	}
}

// decadeExpr — SQL-выражение декады оценки: 79.5 → 70, 85 → 80.
func decadeExpr(col string) string {
	return "CAST(" + col + "/10 AS INTEGER)*10"
}

// decadeBucketLabel — подпись бакета декады оценки.
func decadeBucketLabel(v int64) string { return strconv.FormatInt(v, 10) }

// yearBucketLabel — подпись бакета года; нулевой/отсутствующий год — «—».
func yearBucketLabel(v int64) string {
	if v <= 0 {
		return "—"
	}
	return strconv.FormatInt(v, 10)
}

// valueIndexBuckets — общий расчёт бакетов по числовому SQL-выражению expr
// (только из белого списка, не из пользовательского ввода): строки группируются
// по значению, сортируются как в ORDER BY выдачи (NULL в конец, значение по
// p.Order), из счётчиков собираются кумулятивные смещения. NULL-группа чипа не
// получает — это непокрываемый хвост выдачи.
func valueIndexBuckets(db *sql.DB, p ListParams, expr string, label func(int64) string) ([]IndexBucket, error) {
	NormalizeParams(&p)
	whereSQL, args := buildListWhere(p)
	rows, err := db.Query("SELECT "+expr+", COUNT(*) FROM games "+whereSQL+" GROUP BY 1", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type group struct {
		v sql.NullInt64
		n int
	}
	var groups []group
	for rows.Next() {
		var g group
		if err := rows.Scan(&g.v, &g.n); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	desc := strings.EqualFold(p.Order, "desc")
	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if a.v.Valid != b.v.Valid {
			return a.v.Valid // NULL всегда в конец, как (col IS NULL) в ORDER BY
		}
		if desc {
			return a.v.Int64 > b.v.Int64
		}
		return a.v.Int64 < b.v.Int64
	})

	var buckets []IndexBucket
	offset := 0
	for _, g := range groups {
		if !g.v.Valid {
			break // NULL-хвост: чипов больше не будет
		}
		buckets = append(buckets, IndexBucket{Label: label(g.v.Int64), Offset: offset})
		offset += g.n
	}
	return buckets, nil
}

// TitleIndexBuckets считает бакеты первого символа названия для буквенного
// индекса. Не-латинские первые символы попадают в бакет "#", который при
// сортировке по возрастанию идёт первым (как и в ORDER BY: цифры/символы
// раньше букв), при убывании — последним. Пустые бакеты опускаются.
func TitleIndexBuckets(db *sql.DB, p ListParams) ([]IndexBucket, error) {
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

	var buckets []IndexBucket
	offset := 0
	for _, l := range order {
		n := counts[l]
		if n == 0 {
			continue
		}
		buckets = append(buckets, IndexBucket{Label: l, Offset: offset})
		offset += n
	}
	return buckets, nil
}

var monthNames = []string{"Янв", "Фев", "Мар", "Апр", "Май", "Июн", "Июл", "Авг", "Сен", "Окт", "Ноя", "Дек"}

// AddedIndexBuckets возвращает месячные бакеты для сортировки по текущему
// присутствию в каталоге. Смещение относится ко всей выдаче, а не к странице.
func AddedIndexBuckets(db *sql.DB, p ListParams) ([]IndexBucket, error) {
	NormalizeParams(&p)
	whereSQL, args := buildListWhere(p)
	orderSuffix := ""
	if strings.EqualFold(p.Order, "desc") {
		orderSuffix = " DESC"
	}
	rows, err := db.Query(`
		SELECT COALESCE(strftime('%Y-%m', `+currentCatalogAddedExpr+`), ''), COUNT(*)
		FROM games
		`+whereSQL+`
		GROUP BY 1
		ORDER BY 1`+orderSuffix, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type group struct {
		ym string
		n  int
	}
	var groups []group
	for rows.Next() {
		var g group
		if err := rows.Scan(&g.ym, &g.n); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var buckets []IndexBucket
	offset := 0
	var noDateCount int
	for _, g := range groups {
		if g.ym == "" || len(g.ym) < 7 {
			noDateCount += g.n
			continue
		}
		yr := g.ym[:4]
		mth := g.ym[5:7]
		mn, _ := strconv.Atoi(mth)
		if mn < 1 || mn > 12 {
			noDateCount += g.n
			continue
		}
		label := yr + " " + monthNames[mn-1]
		buckets = append(buckets, IndexBucket{Label: label, Offset: offset})
		offset += g.n
	}
	if noDateCount > 0 {
		buckets = append(buckets, IndexBucket{Label: "Без даты", Offset: offset})
	}

	return buckets, nil
}

// ListGames возвращает отфильтрованную, отсортированную и постранично нарезанную
// выборку игр.
func ListGames(db *sql.DB, p ListParams) (ListResult, error) {
	NormalizeParams(&p)

	whereSQL, args := buildListWhere(p)

	res := ListResult{Page: p.Page, PageSize: p.PageSize}

	if err := db.QueryRow("SELECT COUNT(*) FROM games "+whereSQL, args...).Scan(&res.Total); err != nil {
		return res, err
	}
	res.TotalPages = (res.Total + p.PageSize - 1) / p.PageSize
	// Клампим страницу к [1..TotalPages]: иначе огромный page (напр.
	// 9223372036854775807) переполняет вычисление OFFSET.
	if res.TotalPages > 0 && p.Page > res.TotalPages {
		p.Page = res.TotalPages
	}
	res.Page = p.Page

	col, ok := sortColumns[p.Sort]
	if !ok {
		col = "title"
	}
	dir := "ASC"
	if strings.EqualFold(p.Order, "desc") {
		dir = "DESC"
	}
	// игры без значения сортируемой колонки — всегда в конец
	orderSQL := fmt.Sprintf("ORDER BY (%s IS NULL), %s %s, title COLLATE NOCASE ASC", col, col, dir)

	query := `
SELECT id, title, COALESCE(title_en,''), COALESCE(release_year,0), COALESCE(platforms,''), COALESCE(image_url,''),
       COALESCE(store_url,''), metacritic_score, metacritic_url, metacritic_user_score, metacritic_user_count,
       opencritic_score, opencritic_url, opencritic_player_score, opencritic_player_count,
       average_score, critic_average_score, player_average_score,
       hltb_main_extra, hltb_rating, hltb_url, COALESCE(screen_langs,''), COALESCE(spoken_langs,''),
       ` + currentCatalogAddedExpr + `, ` + currentCatalogAddedSourceExpr + `, ` + currentCatalogSourceURLExpr + `
FROM games ` + whereSQL + " " + orderSQL + " LIMIT ? OFFSET ?"
	args = append(args, p.PageSize, (p.Page-1)*p.PageSize)

	rows, err := db.Query(query, args...)
	if err != nil {
		return res, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var g GameView
		var screenLangs, spokenLangs string
		if err := rows.Scan(&g.ID, &g.Title, &g.TitleEn, &g.ReleaseYear, &g.Platforms, &g.ImageURL,
			&g.StoreURL, &g.Metacritic, &g.MetacriticPageURL, &g.MetacriticUser, &g.MetacriticUserCount,
			&g.OpenCritic, &g.OpenCriticPageURL, &g.OpenCriticPlayer, &g.OpenCriticPlayerCount,
			&g.Average, &g.CriticAverage, &g.PlayerAverage,
			&g.HLTBMainSec, &g.HLTBRating, &g.HLTBPageURL, &screenLangs, &spokenLangs,
			&g.CatalogAddedOn, &g.CatalogAddedSource, &g.CatalogSourceURL); err != nil {
			return res, err
		}
		g.RuSub = strings.Contains(screenLangs, `"ru"`)
		g.RuVoice = strings.Contains(spokenLangs, `"ru"`)
		res.Games = append(res.Games, g)
		ids = append(ids, g.ID)
	}
	if err := rows.Err(); err != nil {
		return res, err
	}

	if err := attachGenres(db, res.Games, ids); err != nil {
		return res, err
	}
	return res, nil
}

// attachGenres дочитывает жанры для выбранных игр одним запросом.
func attachGenres(db *sql.DB, games []GameView, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.Query(
		"SELECT game_id, genre FROM game_genres WHERE game_id IN ("+placeholders+") ORDER BY genre", args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	byID := make(map[string][]string, len(games))
	for rows.Next() {
		var gameID, genre string
		if err := rows.Scan(&gameID, &genre); err != nil {
			return err
		}
		byID[gameID] = append(byID[gameID], genre)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range games {
		games[i].Genres = byID[games[i].ID]
	}
	return nil
}

// DistinctYears возвращает годы выпуска по убыванию (для фильтра).
func DistinctYears(db *sql.DB) ([]int, error) {
	rows, err := db.Query(
		"SELECT DISTINCT release_year FROM games WHERE active = 1 AND release_year > 0 ORDER BY release_year DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var years []int
	for rows.Next() {
		var y int
		if err := rows.Scan(&y); err != nil {
			return nil, err
		}
		years = append(years, y)
	}
	return years, rows.Err()
}

// DistinctGenres возвращает жанры по алфавиту (для фильтра).
func DistinctGenres(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT DISTINCT genre FROM game_genres WHERE game_id IN (SELECT id FROM games WHERE active = 1) ORDER BY genre")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var genres []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		genres = append(genres, g)
	}
	return genres, rows.Err()
}
