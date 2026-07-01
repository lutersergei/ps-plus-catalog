package store

import (
	"database/sql"
	"encoding/json"
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
	rows, err := db.Query(`
SELECT id, title, COALESCE(title_en, title)
FROM games
WHERE active = 1
  AND (
    oc_checked_at IS NULL
    OR oc_checked_at < ?
  )
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

// GamesNeedingHLTB — игры без свежей проверки HowLongToBeat.
func GamesNeedingHLTB(db *sql.DB, staleBefore time.Time) ([]ScoreTarget, error) {
	rows, err := db.Query(`
SELECT id, title, COALESCE(title_en, title)
FROM games
WHERE active = 1
  AND (
    hltb_checked_at IS NULL
    OR hltb_checked_at < ?
  )
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

// LangTarget — игра, которой нужны данные о языках.
type LangTarget struct {
	ID         string
	ConceptURL string // store_url из каталога
}

// GamesNeedingLangs возвращает активные игры без свежей проверки языков.
func GamesNeedingLangs(db *sql.DB, staleBefore time.Time) ([]LangTarget, error) {
	rows, err := db.Query(`
SELECT id, COALESCE(store_url, '')
FROM games
WHERE active = 1
  AND store_url IS NOT NULL AND store_url != ''
  AND (langs_checked_at IS NULL OR langs_checked_at < ?)
ORDER BY title`, staleBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LangTarget
	for rows.Next() {
		var t LangTarget
		if err := rows.Scan(&t.ID, &t.ConceptURL); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateLangs записывает языки озвучки и субтитров (JSON-массивы) и помечает время проверки.
// Пустые срезы (нет данных) хранятся как "[]", не NULL — чтобы не повторять проверку.
func UpdateLangs(db *sql.DB, id string, spoken, screen []string) error {
	if spoken == nil {
		spoken = []string{}
	}
	if screen == nil {
		screen = []string{}
	}
	spokenJSON, _ := json.Marshal(spoken)
	screenJSON, _ := json.Marshal(screen)
	_, err := db.Exec(`UPDATE games SET spoken_langs = ?, screen_langs = ?, langs_checked_at = CURRENT_TIMESTAMP WHERE id = ?`,
		string(spokenJSON), string(screenJSON), id)
	return err
}

// UpdateHLTB записывает время Main+Sides (сек) и рейтинг HLTB (0–100), помечает
// время проверки. Невалидные значения (Valid=false) означают «нет данных».
func UpdateHLTB(db *sql.DB, id string, mainExtra, rating, hltbID sql.NullInt64, hltbURL sql.NullString) error {
	if _, err := db.Exec(`
UPDATE games SET hltb_main_extra = ?, hltb_rating = ?, hltb_id = ?, hltb_url = ?, hltb_checked_at = CURRENT_TIMESTAMP
WHERE id = ?`, mainExtra, rating, hltbID, hltbURL, id); err != nil {
		return err
	}
	return recomputeAverages(db, id)
}

// UpdateMetacritic записывает только critic score Metacritic и оставляет user
// score пустым. Сохранён для старых вызовов и тестов.
func UpdateMetacritic(db *sql.DB, id string, mc sql.NullInt64) error {
	return UpdateMetacriticScores(db, id, mc, sql.NullInt64{}, sql.NullInt64{})
}

// UpdateMetacriticScores записывает Metacritic critic score и user score.
// userCount.Valid=false означает, что число пользовательских оценок неизвестно.
func UpdateMetacriticScores(db *sql.DB, id string, mc, userScore, userCount sql.NullInt64) error {
	if _, err := db.Exec(`
UPDATE games SET metacritic_score = ?, metacritic_user_score = ?, metacritic_user_count = ?, mc_checked_at = CURRENT_TIMESTAMP
WHERE id = ?`, mc, userScore, userCount, id); err != nil {
		return err
	}
	return recomputeAverages(db, id)
}

// UpdateOpenCritic записывает только critic score и URL OpenCritic. Сохранён
// для старых вызовов и тестов.
func UpdateOpenCritic(db *sql.DB, id string, oc sql.NullInt64, ocURL sql.NullString) error {
	return UpdateOpenCriticScores(db, id, oc, ocURL, sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{})
}

// UpdateOpenCriticScores записывает OpenCritic critic score, canonical URL,
// OpenCritic id и Player Rating.
func UpdateOpenCriticScores(db *sql.DB, id string, oc sql.NullInt64, ocURL sql.NullString, ocID, playerScore, playerCount sql.NullInt64) error {
	if _, err := db.Exec(`
UPDATE games SET opencritic_score = ?, opencritic_url = ?, opencritic_id = ?,
                 opencritic_player_score = ?, opencritic_player_count = ?,
                 oc_checked_at = CURRENT_TIMESTAMP
WHERE id = ?`, oc, ocURL, ocID, playerScore, playerCount, id); err != nil {
		return err
	}
	return recomputeAverages(db, id)
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

// averageExpr averages all available score sources. NULL and 0 are treated as
// missing values because upstream APIs may use 0 as "no score".
const averageExpr = `CASE
  WHEN ((COALESCE(metacritic_score,0) > 0) + (COALESCE(metacritic_user_score,0) > 0) + (COALESCE(opencritic_score,0) > 0) + (COALESCE(opencritic_player_score,0) > 0) + (COALESCE(hltb_rating,0) > 0)) = 0 THEN NULL
  ELSE ROUND(
    (CASE WHEN COALESCE(metacritic_score,0) > 0 THEN metacritic_score ELSE 0 END
     + CASE WHEN COALESCE(metacritic_user_score,0) > 0 THEN metacritic_user_score ELSE 0 END
     + CASE WHEN COALESCE(opencritic_score,0) > 0 THEN opencritic_score ELSE 0 END
     + CASE WHEN COALESCE(opencritic_player_score,0) > 0 THEN opencritic_player_score ELSE 0 END
     + CASE WHEN COALESCE(hltb_rating,0) > 0 THEN hltb_rating ELSE 0 END) * 1.0
    / ((COALESCE(metacritic_score,0) > 0) + (COALESCE(metacritic_user_score,0) > 0) + (COALESCE(opencritic_score,0) > 0) + (COALESCE(opencritic_player_score,0) > 0) + (COALESCE(hltb_rating,0) > 0)), 1)
END`

const criticAverageExpr = `CASE
  WHEN ((COALESCE(metacritic_score,0) > 0) + (COALESCE(opencritic_score,0) > 0)) = 0 THEN NULL
  ELSE ROUND(
    (CASE WHEN COALESCE(metacritic_score,0) > 0 THEN metacritic_score ELSE 0 END
     + CASE WHEN COALESCE(opencritic_score,0) > 0 THEN opencritic_score ELSE 0 END) * 1.0
    / ((COALESCE(metacritic_score,0) > 0) + (COALESCE(opencritic_score,0) > 0)), 1)
END`

const playerAverageExpr = `CASE
  WHEN ((COALESCE(metacritic_user_score,0) > 0) + (COALESCE(opencritic_player_score,0) > 0) + (COALESCE(hltb_rating,0) > 0)) = 0 THEN NULL
  ELSE ROUND(
    (CASE WHEN COALESCE(metacritic_user_score,0) > 0 THEN metacritic_user_score ELSE 0 END
     + CASE WHEN COALESCE(opencritic_player_score,0) > 0 THEN opencritic_player_score ELSE 0 END
     + CASE WHEN COALESCE(hltb_rating,0) > 0 THEN hltb_rating ELSE 0 END) * 1.0
    / ((COALESCE(metacritic_user_score,0) > 0) + (COALESCE(opencritic_player_score,0) > 0) + (COALESCE(hltb_rating,0) > 0)), 1)
END`

// recomputeAverages пересчитывает все сохранённые сводные оценки строки.
func recomputeAverages(db *sql.DB, id string) error {
	_, err := db.Exec(`
UPDATE games
SET average_score = (`+averageExpr+`),
    critic_average_score = (`+criticAverageExpr+`),
    player_average_score = (`+playerAverageExpr+`)
WHERE id = ?`, id)
	return err
}

// RecomputeAllAverages пересчитывает сводные оценки у всех игр после изменения
// формул или массового обновления оценок.
func RecomputeAllAverages(db *sql.DB) error {
	_, err := db.Exec(`
UPDATE games
SET average_score = (` + averageExpr + `),
    critic_average_score = (` + criticAverageExpr + `),
    player_average_score = (` + playerAverageExpr + `)`)
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
