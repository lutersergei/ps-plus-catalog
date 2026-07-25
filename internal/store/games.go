package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// dbHandle — общий интерфейс для *sql.DB и *sql.Tx: позволяет вызывать
// операции записи из обычного соединения или внутри внешней транзакции.
type dbHandle interface {
	Exec(query string, args ...any) (sql.Result, error)
	Prepare(query string) (*sql.Stmt, error)
	QueryRow(query string, args ...any) *sql.Row
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

// SourceGenre — жанр, полученный из конкретного внешнего источника.
type SourceGenre struct {
	Genre         string
	SourceGenreID sql.NullInt64
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

// CatalogSnapshotResult описывает изменения членства в каталоге между двумя
// успешными снимками. Initial означает первый снимок, для которого нет
// предыдущего состояния и потому нельзя честно назвать дату добавления.
type CatalogSnapshotResult struct {
	Initial bool
	Added   int64
	Removed int64
}

// RecordCatalogSnapshot обновляет периоды присутствия игр в PS Plus Extra.
//
// Для первого снимка added_on остаётся NULL: наличие игры в момент первого
// наблюдения не доказывает, что её добавили в этот день. На следующих снимках
// новые и вернувшиеся игры получают observedOn и источник "observed". Более
// точную дату из официального анонса можно записать через SetCatalogAddedDate.
func RecordCatalogSnapshot(db dbHandle, presentIDs []string, observedAt time.Time) (CatalogSnapshotResult, error) {
	var result CatalogSnapshotResult
	if len(presentIDs) == 0 {
		return result, nil
	}

	var periods int
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_memberships`).Scan(&periods); err != nil {
		return result, err
	}
	result.Initial = periods == 0

	firstSeen := observedAt.UTC().Format(time.RFC3339Nano)
	observedOn := observedAt.UTC().Format("2006-01-02")

	insert, err := db.Prepare(`
INSERT INTO catalog_memberships
	(game_id, added_on, first_seen_at, last_seen_at, added_on_source)
SELECT ?, ?, ?, ?, ?
WHERE NOT EXISTS (
	SELECT 1 FROM catalog_memberships WHERE game_id = ? AND removed_on IS NULL
)`)
	if err != nil {
		return result, err
	}
	defer insert.Close()

	var addedOn, source any
	if !result.Initial {
		addedOn = observedOn
		source = "observed"
	}
	for _, id := range presentIDs {
		res, err := insert.Exec(id, addedOn, firstSeen, firstSeen, source, id)
		if err != nil {
			return result, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return result, err
		}
		result.Added += n
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(presentIDs)), ",")
	presentArgs := make([]any, 0, len(presentIDs)+1)
	presentArgs = append(presentArgs, firstSeen)
	for _, id := range presentIDs {
		presentArgs = append(presentArgs, id)
	}
	if _, err := db.Exec(`
UPDATE catalog_memberships
SET last_seen_at = ?
WHERE removed_on IS NULL AND game_id IN (`+placeholders+`)`, presentArgs...); err != nil {
		return result, err
	}

	missingArgs := make([]any, 0, len(presentIDs)+1)
	missingArgs = append(missingArgs, observedOn)
	for _, id := range presentIDs {
		missingArgs = append(missingArgs, id)
	}
	res, err := db.Exec(`
UPDATE catalog_memberships
SET removed_on = ?
WHERE removed_on IS NULL AND game_id NOT IN (`+placeholders+`)`, missingArgs...)
	if err != nil {
		return result, err
	}
	result.Removed, err = res.RowsAffected()
	return result, err
}

// SetCatalogAddedDate заменяет наблюдаемую дату текущего периода на точную
// дату из внешнего источника (обычно официального PlayStation Blog). Источник
// обязателен, чтобы вызывающий код не мог молча потерять обновление.
func SetCatalogAddedDate(db dbHandle, gameID string, addedOn time.Time, source, sourceURL string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("catalog added date source is required")
	}
	result, err := db.Exec(`
UPDATE catalog_memberships
SET added_on = ?, added_on_source = ?, source_url = ?
WHERE id = (
	SELECT id FROM catalog_memberships
	WHERE game_id = ? AND removed_on IS NULL
	ORDER BY first_seen_at DESC
	LIMIT 1
)`, addedOn.UTC().Format("2006-01-02"), source, strings.TrimSpace(sourceURL), gameID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ScoreTarget — игра, которой нужны/устарели оценки.
type ScoreTarget struct {
	ID                         string
	Title                      string
	TitleEn                    string
	NeedsMetacriticURLBackfill bool
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

// GamesNeedingMetacritic — игры без свежей проверки Metacritic, а также свежие
// оценённые игры без сохранённой ссылки на найденную страницу. Второе условие
// заполняет URL у строк, собранных до появления metacritic_url, не меняя
// политику обычного обновления устаревших оценок.
func GamesNeedingMetacritic(db *sql.DB, staleBefore time.Time) ([]ScoreTarget, error) {
	rows, err := db.Query(`
SELECT id, title, COALESCE(title_en, title),
       metacritic_score IS NOT NULL
       AND (metacritic_url IS NULL OR TRIM(metacritic_url) = '')
       AND mc_checked_at IS NOT NULL
       AND mc_checked_at >= ?
FROM games
WHERE active = 1
  AND (
    mc_checked_at IS NULL
    OR mc_checked_at < ?
    OR (
      metacritic_score IS NOT NULL
      AND (metacritic_url IS NULL OR TRIM(metacritic_url) = '')
    )
  )
ORDER BY title`, staleBefore, staleBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ScoreTarget
	for rows.Next() {
		var t ScoreTarget
		if err := rows.Scan(&t.ID, &t.Title, &t.TitleEn, &t.NeedsMetacriticURLBackfill); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
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

// SetSourceGenres заменяет жанры игры для одного источника. Пустые жанры
// игнорируются; жанры других источников для этой игры не затрагиваются.
func SetSourceGenres(db dbHandle, gameID, source string, genres []SourceGenre) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil
	}
	if _, err := db.Exec(`DELETE FROM game_source_genres WHERE game_id = ? AND source = ?`, gameID, source); err != nil {
		return err
	}
	stmt, err := db.Prepare(`INSERT OR IGNORE INTO game_source_genres (game_id, source, genre, source_genre_id) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, g := range genres {
		genre := strings.TrimSpace(g.Genre)
		if genre == "" {
			continue
		}
		if _, err := stmt.Exec(gameID, source, genre, g.SourceGenreID); err != nil {
			return err
		}
	}
	return nil
}

// SourceGenres возвращает сохранённые жанры, сгруппированные по источнику.
func SourceGenres(db *sql.DB, gameID string) (map[string][]string, error) {
	rows, err := db.Query(`
SELECT source, genre
FROM game_source_genres
WHERE game_id = ?
ORDER BY source, genre`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var source, genre string
		if err := rows.Scan(&source, &genre); err != nil {
			return nil, err
		}
		out[source] = append(out[source], genre)
	}
	return out, rows.Err()
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
	return UpdateMetacriticScores(db, id, mc, sql.NullInt64{}, sql.NullInt64{}, sql.NullString{})
}

// UpdateMetacriticScores записывает Metacritic critic score и user score.
// userCount.Valid=false означает, что число пользовательских оценок неизвестно.
func UpdateMetacriticScores(db *sql.DB, id string, mc, userScore, userCount sql.NullInt64, pageURL sql.NullString) error {
	if _, err := db.Exec(`
UPDATE games SET metacritic_score = ?, metacritic_url = ?, metacritic_user_score = ?, metacritic_user_count = ?, mc_checked_at = CURRENT_TIMESTAMP
WHERE id = ?`, mc, pageURL, userScore, userCount, id); err != nil {
		return err
	}
	return recomputeAverages(db, id)
}

// UpdateMetacriticPageURL сохраняет URL уже найденной страницы, не меняя
// оценки или время их проверки. Используется для безопасного backfill старых
// строк, у которых оценка есть, а ссылка не была сохранена.
func UpdateMetacriticPageURL(db *sql.DB, id string, pageURL sql.NullString) error {
	_, err := db.Exec(`UPDATE games SET metacritic_url = ? WHERE id = ?`, pageURL, id)
	return err
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

// openCriticPlayerWeightExpr returns the source weight for OpenCritic Player
// Rating. With two other player sources available, 0.5 gives OpenCritic a 20%
// share of the player average: w / (1 + 1 + w) = 0.20.
const openCriticPlayerWeightExpr = `CASE
  WHEN COALESCE(opencritic_player_score,0) <= 0 OR COALESCE(opencritic_player_count,0) < 20 THEN 0.0
  WHEN COALESCE(opencritic_player_count,0) > 100 THEN 1.0
  ELSE 0.5
END`

// averageExpr averages all available score sources. NULL and 0 are treated as
// missing values because upstream APIs may use 0 as "no score". OpenCritic
// Player Rating is additionally weighted by its vote count.
const averageExpr = `CASE
  WHEN ((COALESCE(metacritic_score,0) > 0) + (COALESCE(metacritic_user_score,0) > 0) + (COALESCE(opencritic_score,0) > 0) + (` + openCriticPlayerWeightExpr + `) + (COALESCE(hltb_rating,0) > 0)) = 0 THEN NULL
  ELSE ROUND(
    (CASE WHEN COALESCE(metacritic_score,0) > 0 THEN metacritic_score ELSE 0 END
     + CASE WHEN COALESCE(metacritic_user_score,0) > 0 THEN metacritic_user_score ELSE 0 END
     + CASE WHEN COALESCE(opencritic_score,0) > 0 THEN opencritic_score ELSE 0 END
     + COALESCE(opencritic_player_score,0) * (` + openCriticPlayerWeightExpr + `)
     + CASE WHEN COALESCE(hltb_rating,0) > 0 THEN hltb_rating ELSE 0 END) * 1.0
    / ((COALESCE(metacritic_score,0) > 0) + (COALESCE(metacritic_user_score,0) > 0) + (COALESCE(opencritic_score,0) > 0) + (` + openCriticPlayerWeightExpr + `) + (COALESCE(hltb_rating,0) > 0)), 1)
END`

const criticAverageExpr = `CASE
  WHEN ((COALESCE(metacritic_score,0) > 0) + (COALESCE(opencritic_score,0) > 0)) = 0 THEN NULL
  ELSE ROUND(
    (CASE WHEN COALESCE(metacritic_score,0) > 0 THEN metacritic_score ELSE 0 END
     + CASE WHEN COALESCE(opencritic_score,0) > 0 THEN opencritic_score ELSE 0 END) * 1.0
    / ((COALESCE(metacritic_score,0) > 0) + (COALESCE(opencritic_score,0) > 0)), 1)
END`

const playerAverageExpr = `CASE
  WHEN ((COALESCE(metacritic_user_score,0) > 0) + (` + openCriticPlayerWeightExpr + `) + (COALESCE(hltb_rating,0) > 0)) = 0 THEN NULL
  ELSE ROUND(
    (CASE WHEN COALESCE(metacritic_user_score,0) > 0 THEN metacritic_user_score ELSE 0 END
     + COALESCE(opencritic_player_score,0) * (` + openCriticPlayerWeightExpr + `)
     + CASE WHEN COALESCE(hltb_rating,0) > 0 THEN hltb_rating ELSE 0 END) * 1.0
    / ((COALESCE(metacritic_user_score,0) > 0) + (` + openCriticPlayerWeightExpr + `) + (COALESCE(hltb_rating,0) > 0)), 1)
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
