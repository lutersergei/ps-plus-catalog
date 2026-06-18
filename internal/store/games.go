package store

import (
	"database/sql"
	"strings"
	"time"
)

// GameRow — данные каталога одной игры для записи в БД (без оценок).
type GameRow struct {
	ID          string
	Title       string
	TitleEn     string
	ReleaseYear int
	Genres      []string
	Platforms   []string
	ImageURL    string
	StoreURL    string
}

// UpsertGame вставляет или обновляет поля каталога игры. Поля оценок
// (metacritic_score, opencritic_score, average_score, scores_updated_at) НЕ
// затрагиваются, чтобы повторный sync не сбрасывал уже собранные оценки.
func UpsertGame(db *sql.DB, g GameRow) error {
	_, err := db.Exec(`
INSERT INTO games (id, title, title_en, release_year, platforms, image_url, store_url)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  title=excluded.title,
  title_en=excluded.title_en,
  release_year=excluded.release_year,
  platforms=excluded.platforms,
  image_url=excluded.image_url,
  store_url=excluded.store_url`,
		g.ID, g.Title, g.TitleEn, g.ReleaseYear,
		strings.Join(g.Platforms, ", "), g.ImageURL, g.StoreURL)
	return err
}

// ScoreTarget — игра, которой нужны/устарели оценки.
type ScoreTarget struct {
	ID      string
	Title   string
	TitleEn string
}

// GamesNeedingScores возвращает игры без оценок или с устаревшими (раньше
// staleBefore). Игры с уже свежими scores_updated_at пропускаются.
func GamesNeedingScores(db *sql.DB, staleBefore time.Time) ([]ScoreTarget, error) {
	rows, err := db.Query(`
SELECT id, title, COALESCE(title_en, title)
FROM games
WHERE scores_updated_at IS NULL OR scores_updated_at < ?
ORDER BY title`, staleBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScoreTarget
	for rows.Next() {
		var t ScoreTarget
		if err := rows.Scan(&t.ID, &t.Title, &t.TitleEn); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateScores записывает оценки игры и помечает время обновления.
func UpdateScores(db *sql.DB, id string, mc, oc sql.NullInt64, avg sql.NullFloat64) error {
	_, err := db.Exec(`
UPDATE games
SET metacritic_score = ?, opencritic_score = ?, average_score = ?, scores_updated_at = CURRENT_TIMESTAMP
WHERE id = ?`, mc, oc, avg, id)
	return err
}

// SetGenres заменяет жанры игры на переданный список.
func SetGenres(db *sql.DB, gameID string, genres []string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM game_genres WHERE game_id = ?`, gameID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO game_genres (game_id, genre) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, genre := range genres {
		if _, err := stmt.Exec(gameID, genre); err != nil {
			return err
		}
	}
	return tx.Commit()
}
