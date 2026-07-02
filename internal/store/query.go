package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	"github.com/lutersergei/ps-plus-catalog/internal/scores"
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
	HLTBFromHours float64  // нижняя граница Main+Sides в часах (0 = не задана)
	HLTBToHours   float64  // верхняя граница Main+Sides в часах (0 = не задана)
	Sort          string   // "year" | "average" | "critic" | "player" | "title" | "hltbmain"
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

// MetacriticURL — прямая ссылка на страницу игры (slug строится из английского
// названия той же логикой, что и при сборе оценки). Если slug пуст — поиск.
func (g GameView) MetacriticURL() string {
	t := g.TitleEn
	if t == "" {
		t = g.Title
	}
	if slug := scores.MetacriticSlug(t); slug != "" {
		return "https://www.metacritic.com/game/" + slug + "/"
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
	"title":    "title",
	"hltbmain": "hltb_main_extra",
}

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
	if p.HLTBFromHours > 0 && p.HLTBToHours > 0 && p.HLTBToHours < p.HLTBFromHours {
		p.HLTBFromHours, p.HLTBToHours = 0, 0
	}
}

// ListGames возвращает отфильтрованную, отсортированную и постранично нарезанную
// выборку игр.
func ListGames(db *sql.DB, p ListParams) (ListResult, error) {
	NormalizeParams(&p)

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

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

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
	orderSQL := fmt.Sprintf("ORDER BY (%s IS NULL), %s %s, title ASC", col, col, dir)

	query := `
SELECT id, title, COALESCE(title_en,''), COALESCE(release_year,0), COALESCE(platforms,''), COALESCE(image_url,''),
       COALESCE(store_url,''), metacritic_score, metacritic_user_score, metacritic_user_count,
       opencritic_score, opencritic_url, opencritic_player_score, opencritic_player_count,
       average_score, critic_average_score, player_average_score,
       hltb_main_extra, hltb_rating, hltb_url, COALESCE(screen_langs,''), COALESCE(spoken_langs,'')
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
			&g.StoreURL, &g.Metacritic, &g.MetacriticUser, &g.MetacriticUserCount,
			&g.OpenCritic, &g.OpenCriticPageURL, &g.OpenCriticPlayer, &g.OpenCriticPlayerCount,
			&g.Average, &g.CriticAverage, &g.PlayerAverage,
			&g.HLTBMainSec, &g.HLTBRating, &g.HLTBPageURL, &screenLangs, &spokenLangs); err != nil {
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
