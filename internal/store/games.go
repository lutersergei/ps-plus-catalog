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

// gamesNeeding возвращает игры, у которых указанная колонка-отметка проверки
// (checkedCol) пуста или старее staleBefore.
func gamesNeeding(db *sql.DB, checkedCol string, staleBefore time.Time) ([]ScoreTarget, error) {
	rows, err := db.Query(`
SELECT id, title, COALESCE(title_en, title)
FROM games
WHERE `+checkedCol+` IS NULL OR `+checkedCol+` < ?
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

// GamesNeedingMetacritic — игры без свежей проверки Metacritic.
func GamesNeedingMetacritic(db *sql.DB, staleBefore time.Time) ([]ScoreTarget, error) {
	return gamesNeeding(db, "mc_checked_at", staleBefore)
}

// GamesNeedingOpenCritic — игры без свежей проверки OpenCritic.
func GamesNeedingOpenCritic(db *sql.DB, staleBefore time.Time) ([]ScoreTarget, error) {
	return gamesNeeding(db, "oc_checked_at", staleBefore)
}

// GamesNeedingHLTB — игры без свежей проверки HowLongToBeat.
func GamesNeedingHLTB(db *sql.DB, staleBefore time.Time) ([]ScoreTarget, error) {
	return gamesNeeding(db, "hltb_checked_at", staleBefore)
}

// UpdateHLTB записывает время Main+Sides (сек) и рейтинг HLTB (0–100), помечает
// время проверки. Невалидные значения (Valid=false) означают «нет данных».
func UpdateHLTB(db *sql.DB, id string, mainExtra, rating sql.NullInt64) error {
	_, err := db.Exec(`
UPDATE games SET hltb_main_extra = ?, hltb_rating = ?, hltb_checked_at = CURRENT_TIMESTAMP
WHERE id = ?`, mainExtra, rating, id)
	return err
}

// UpdateMetacritic записывает оценку Metacritic (или NULL, если не найдена),
// помечает время проверки и пересчитывает среднее. mc.Valid=false означает,
// что проверка была, но оценки нет.
func UpdateMetacritic(db *sql.DB, id string, mc sql.NullInt64) error {
	if _, err := db.Exec(`UPDATE games SET metacritic_score = ?, mc_checked_at = CURRENT_TIMESTAMP WHERE id = ?`, mc, id); err != nil {
		return err
	}
	return recomputeAverage(db, id)
}

// UpdateOpenCritic — то же для OpenCritic.
func UpdateOpenCritic(db *sql.DB, id string, oc sql.NullInt64) error {
	if _, err := db.Exec(`UPDATE games SET opencritic_score = ?, oc_checked_at = CURRENT_TIMESTAMP WHERE id = ?`, oc, id); err != nil {
		return err
	}
	return recomputeAverage(db, id)
}

// ResetMissingChecks сбрасывает отметки проверки у игр без соответствующей оценки,
// чтобы их перепроверили в следующем sync (например, после улучшения матчинга).
// Возвращает число затронутых строк по каждому источнику.
func ResetMissingChecks(db *sql.DB) (mc, oc int64, err error) {
	r1, err := db.Exec(`UPDATE games SET mc_checked_at = NULL WHERE metacritic_score IS NULL`)
	if err != nil {
		return 0, 0, err
	}
	r2, err := db.Exec(`UPDATE games SET oc_checked_at = NULL WHERE opencritic_score IS NULL`)
	if err != nil {
		return 0, 0, err
	}
	if _, err := db.Exec(`UPDATE games SET hltb_checked_at = NULL WHERE hltb_main_extra IS NULL AND hltb_rating IS NULL`); err != nil {
		return 0, 0, err
	}
	mc, _ = r1.RowsAffected()
	oc, _ = r2.RowsAffected()
	return mc, oc, nil
}

// recomputeAverage пересчитывает average_score из текущих значений оценок строки.
func recomputeAverage(db *sql.DB, id string) error {
	_, err := db.Exec(`
UPDATE games SET average_score = (
  CASE
    WHEN metacritic_score IS NOT NULL AND opencritic_score IS NOT NULL
      THEN ROUND((metacritic_score + opencritic_score) / 2.0, 1)
    WHEN metacritic_score IS NOT NULL THEN metacritic_score
    WHEN opencritic_score IS NOT NULL THEN opencritic_score
    ELSE NULL
  END
) WHERE id = ?`, id)
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
