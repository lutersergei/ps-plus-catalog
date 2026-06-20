package store

import (
	"database/sql"
	"strings"
	"time"
)

// dbHandle — общий интерфейс для *sql.DB и *sql.Tx: позволяет вызывать
// операции записи из обычного соединения или внутри внешней транзакции.
type dbHandle interface {
	Exec(query string, args ...any) (sql.Result, error)
	Prepare(query string) (*sql.Stmt, error)
}

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
// (metacritic_score, opencritic_score, hltb_*, average_score) НЕ затрагиваются,
// чтобы повторный sync не сбрасывал уже собранные оценки.
// active всегда выставляется в 1 — игра присутствует в текущем снимке.
func UpsertGame(db dbHandle, g GameRow) error {
	_, err := db.Exec(`
INSERT INTO games (id, title, title_en, release_year, platforms, image_url, store_url, active)
VALUES (?, ?, ?, ?, ?, ?, ?, 1)
ON CONFLICT(id) DO UPDATE SET
  title=excluded.title,
  title_en=excluded.title_en,
  release_year=excluded.release_year,
  platforms=excluded.platforms,
  image_url=excluded.image_url,
  store_url=excluded.store_url,
  active=1`,
		g.ID, g.Title, g.TitleEn, g.ReleaseYear,
		strings.Join(g.Platforms, ", "), g.ImageURL, g.StoreURL)
	return err
}

// DeactivateMissing помечает active=0 все игры, чьи ID не входят в переданный
// список (игры, покинувшие текущий снимок PS Plus). Возвращает число деактивированных.
func DeactivateMissing(db dbHandle, presentIDs []string) (int64, error) {
	if len(presentIDs) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(presentIDs)), ",")
	args := make([]any, len(presentIDs))
	for i, id := range presentIDs {
		args[i] = id
	}
	res, err := db.Exec("UPDATE games SET active = 0 WHERE active = 1 AND id NOT IN ("+placeholders+")", args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountActive возвращает число игр, помеченных active=1 (текущий снимок каталога).
func CountActive(db *sql.DB) (int, error) {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM games WHERE active = 1`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
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
WHERE active = 1
  AND (`+checkedCol+` IS NULL OR `+checkedCol+` < ?)
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
	if _, err := db.Exec(`
UPDATE games SET hltb_main_extra = ?, hltb_rating = ?, hltb_checked_at = CURRENT_TIMESTAMP
WHERE id = ?`, mainExtra, rating, id); err != nil {
		return err
	}
	return recomputeAverage(db, id)
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
	mc, err = r1.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	oc, err = r2.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	return mc, oc, nil
}

// averageExpr — выражение среднего по доступным оценкам: Metacritic, OpenCritic
// и рейтинг HLTB (все в шкале 0–100). NULL, если нет ни одной.
const averageExpr = `CASE
  WHEN ((metacritic_score IS NOT NULL) + (opencritic_score IS NOT NULL) + (hltb_rating IS NOT NULL)) = 0 THEN NULL
  ELSE ROUND(
    (COALESCE(metacritic_score,0) + COALESCE(opencritic_score,0) + COALESCE(hltb_rating,0)) * 1.0
    / ((metacritic_score IS NOT NULL) + (opencritic_score IS NOT NULL) + (hltb_rating IS NOT NULL)), 1)
END`

// recomputeAverage пересчитывает average_score из текущих значений оценок строки.
func recomputeAverage(db *sql.DB, id string) error {
	_, err := db.Exec(`UPDATE games SET average_score = (`+averageExpr+`) WHERE id = ?`, id)
	return err
}

// RecomputeAllAverages пересчитывает среднее у всех игр (после изменения формулы
// или массового обновления оценок).
func RecomputeAllAverages(db *sql.DB) error {
	_, err := db.Exec(`UPDATE games SET average_score = (` + averageExpr + `)`)
	return err
}

// SetGenres заменяет жанры игры на переданный список. Принимает *sql.DB или
// *sql.Tx — транзакционностью управляет вызывающий код.
func SetGenres(db dbHandle, gameID string, genres []string) error {
	if _, err := db.Exec(`DELETE FROM game_genres WHERE game_id = ?`, gameID); err != nil {
		return err
	}
	stmt, err := db.Prepare(`INSERT OR IGNORE INTO game_genres (game_id, genre) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, genre := range genres {
		if _, err := stmt.Exec(gameID, genre); err != nil {
			return err
		}
	}
	return nil
}
