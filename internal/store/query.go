package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// ListParams — параметры выборки игр для страницы.
type ListParams struct {
	Genre    string // фильтр по жанру (пусто = все)
	Year     int    // фильтр по году (0 = все)
	Sort     string // "year" | "average" | "title"
	Order    string // "asc" | "desc"
	Page     int    // с 1
	PageSize int
}

// GameView — игра для отображения.
type GameView struct {
	ID          string
	Title       string
	ReleaseYear int
	Genres      []string
	Platforms   string
	ImageURL    string
	StoreURL    string
	Metacritic  sql.NullInt64
	OpenCritic  sql.NullInt64
	Average     sql.NullFloat64
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
	"year":    "release_year",
	"average": "average_score",
	"title":   "title",
}

// ListGames возвращает отфильтрованную, отсортированную и постранично нарезанную
// выборку игр.
func ListGames(db *sql.DB, p ListParams) (ListResult, error) {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = 24
	}

	var where []string
	var args []any
	if p.Year > 0 {
		where = append(where, "release_year = ?")
		args = append(args, p.Year)
	}
	if p.Genre != "" {
		where = append(where, "id IN (SELECT game_id FROM game_genres WHERE genre = ?)")
		args = append(args, p.Genre)
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
SELECT id, title, COALESCE(release_year,0), COALESCE(platforms,''), COALESCE(image_url,''),
       COALESCE(store_url,''), metacritic_score, opencritic_score, average_score
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
		if err := rows.Scan(&g.ID, &g.Title, &g.ReleaseYear, &g.Platforms, &g.ImageURL,
			&g.StoreURL, &g.Metacritic, &g.OpenCritic, &g.Average); err != nil {
			return res, err
		}
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
		"SELECT DISTINCT release_year FROM games WHERE release_year > 0 ORDER BY release_year DESC")
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
	rows, err := db.Query("SELECT DISTINCT genre FROM game_genres ORDER BY genre")
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
