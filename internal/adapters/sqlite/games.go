package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

// dbHandle — минимальный общий контракт *sql.DB и *sql.Tx для транзакционных
// вспомогательных функций адаптера.
type dbHandle interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (r *Repository) CountActive(ctx context.Context) (int, error) {
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM games WHERE active = 1`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active games: %w", err)
	}
	return count, nil
}

// ApplyCatalogSnapshot атомарно обновляет каталог и жанры, записывает периоды
// присутствия и деактивирует отсутствующие продукты.
func (r *Repository) ApplyCatalogSnapshot(
	ctx context.Context,
	games []domain.CatalogGame,
	observedAt time.Time,
) (domain.CatalogSnapshotResult, error) {
	var result domain.CatalogSnapshotResult
	if len(games) == 0 {
		return result, fmt.Errorf("apply catalog snapshot: empty catalog")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin catalog snapshot: %w", err)
	}
	defer tx.Rollback()
	if err := acquireCatalogSyncLock(ctx, tx); err != nil {
		return result, err
	}

	ids := make([]string, 0, len(games))
	for _, game := range games {
		if err := upsertGame(ctx, tx, game); err != nil {
			return result, fmt.Errorf("upsert game %s: %w", game.ID, err)
		}
		if err := setGenres(ctx, tx, game.ID, game.Genres); err != nil {
			return result, fmt.Errorf("replace genres for %s: %w", game.ID, err)
		}
		sourceGenres := make([]domain.SourceGenre, 0, len(game.Genres))
		for _, genre := range game.Genres {
			sourceGenres = append(sourceGenres, domain.SourceGenre{Name: genre})
		}
		if err := setSourceGenres(ctx, tx, game.ID, "psstore", sourceGenres); err != nil {
			return result, fmt.Errorf("replace psstore genres for %s: %w", game.ID, err)
		}
		ids = append(ids, game.ID)
	}

	membership, err := recordCatalogSnapshot(ctx, tx, ids, observedAt)
	if err != nil {
		return result, fmt.Errorf("record catalog membership: %w", err)
	}
	result.Initial = membership.Initial
	result.Added = membership.Added
	result.Removed = membership.Removed

	result.Deactivated, err = deactivateMissing(ctx, tx, ids)
	if err != nil {
		return result, fmt.Errorf("deactivate missing games: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit catalog snapshot: %w", err)
	}
	return result, nil
}

func upsertGame(ctx context.Context, db dbHandle, game domain.CatalogGame) error {
	_, err := db.ExecContext(ctx, `
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
		game.ID,
		game.Title,
		game.TitleEn,
		game.ReleaseYear,
		strings.Join(game.Platforms, ", "),
		game.ImageURL,
		game.StoreURL,
	)
	return err
}

func deactivateMissing(ctx context.Context, db dbHandle, presentIDs []string) (int64, error) {
	if len(presentIDs) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(presentIDs)), ",")
	args := stringsToAny(presentIDs)
	res, err := db.ExecContext(ctx,
		"UPDATE games SET active = 0 WHERE active = 1 AND id NOT IN ("+placeholders+")",
		args...,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func acquireCatalogSyncLock(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
UPDATE catalog_sync_lock
SET acquired_at = CURRENT_TIMESTAMP
WHERE id = 1`); err != nil {
		return fmt.Errorf("acquire catalog sync lock: %w", err)
	}
	return nil
}

func recordCatalogSnapshot(
	ctx context.Context,
	db dbHandle,
	presentIDs []string,
	observedAt time.Time,
) (domain.CatalogSnapshotResult, error) {
	var result domain.CatalogSnapshotResult
	if len(presentIDs) == 0 {
		return result, nil
	}

	var periods int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_memberships`).Scan(&periods); err != nil {
		return result, err
	}
	result.Initial = periods == 0

	firstSeen := observedAt.UTC().Format(time.RFC3339Nano)
	observedOn := observedAt.UTC().Format("2006-01-02")
	insert, err := db.PrepareContext(ctx, `
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
		res, err := insert.ExecContext(ctx, id, addedOn, firstSeen, firstSeen, source, id)
		if err != nil {
			return result, err
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return result, err
		}
		result.Added += rows
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(presentIDs)), ",")
	presentArgs := make([]any, 0, len(presentIDs)+1)
	presentArgs = append(presentArgs, firstSeen)
	presentArgs = append(presentArgs, stringsToAny(presentIDs)...)
	if _, err := db.ExecContext(ctx, `
UPDATE catalog_memberships
SET last_seen_at = ?
WHERE removed_on IS NULL AND game_id IN (`+placeholders+`)`, presentArgs...); err != nil {
		return result, err
	}

	missingArgs := make([]any, 0, len(presentIDs)+1)
	missingArgs = append(missingArgs, observedOn)
	missingArgs = append(missingArgs, stringsToAny(presentIDs)...)
	res, err := db.ExecContext(ctx, `
UPDATE catalog_memberships
SET removed_on = ?
WHERE removed_on IS NULL AND game_id NOT IN (`+placeholders+`)`, missingArgs...)
	if err != nil {
		return result, err
	}
	result.Removed, err = res.RowsAffected()
	return result, err
}

func stringsToAny(values []string) []any {
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return args
}

func (r *Repository) GamesNeedingMetacritic(ctx context.Context, staleBefore time.Time) ([]domain.ScoreTarget, error) {
	rows, err := r.db.QueryContext(ctx, `
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
		return nil, fmt.Errorf("query Metacritic targets: %w", err)
	}
	defer rows.Close()

	var targets []domain.ScoreTarget
	for rows.Next() {
		var target domain.ScoreTarget
		if err := rows.Scan(
			&target.ID,
			&target.Title,
			&target.TitleEn,
			&target.NeedsMetacriticURLBackfill,
		); err != nil {
			return nil, fmt.Errorf("scan Metacritic target: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query Metacritic targets: %w", err)
	}
	return targets, nil
}

func (r *Repository) GamesNeedingOpenCritic(ctx context.Context, staleBefore time.Time) ([]domain.ScoreTarget, error) {
	return r.scoreTargets(ctx, "oc_checked_at", staleBefore)
}

func (r *Repository) GamesNeedingHLTB(ctx context.Context, staleBefore time.Time) ([]domain.ScoreTarget, error) {
	return r.scoreTargets(ctx, "hltb_checked_at", staleBefore)
}

func (r *Repository) scoreTargets(ctx context.Context, checkedColumn string, staleBefore time.Time) ([]domain.ScoreTarget, error) {
	// Имя столбца приходит только из двух закрытых методов выше; значения
	// пользователя по-прежнему передаются отдельными SQL-параметрами.
	rows, err := r.db.QueryContext(ctx, `
SELECT id, title, COALESCE(title_en, title)
FROM games
WHERE active = 1
  AND (`+checkedColumn+` IS NULL OR `+checkedColumn+` < ?)
ORDER BY title`, staleBefore)
	if err != nil {
		return nil, fmt.Errorf("query score targets: %w", err)
	}
	defer rows.Close()

	var targets []domain.ScoreTarget
	for rows.Next() {
		var target domain.ScoreTarget
		if err := rows.Scan(&target.ID, &target.Title, &target.TitleEn); err != nil {
			return nil, fmt.Errorf("scan score target: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query score targets: %w", err)
	}
	return targets, nil
}

func (r *Repository) GamesNeedingLanguages(ctx context.Context, staleBefore time.Time) ([]domain.LanguageTarget, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, COALESCE(store_url, '')
FROM games
WHERE active = 1
  AND store_url IS NOT NULL AND store_url != ''
  AND (langs_checked_at IS NULL OR langs_checked_at < ?)
ORDER BY title`, staleBefore)
	if err != nil {
		return nil, fmt.Errorf("query language targets: %w", err)
	}
	defer rows.Close()

	var targets []domain.LanguageTarget
	for rows.Next() {
		var target domain.LanguageTarget
		if err := rows.Scan(&target.ID, &target.ConceptURL); err != nil {
			return nil, fmt.Errorf("scan language target: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query language targets: %w", err)
	}
	return targets, nil
}

func (r *Repository) UpdateLanguages(ctx context.Context, id string, spoken, screen []string) error {
	if spoken == nil {
		spoken = []string{}
	}
	if screen == nil {
		screen = []string{}
	}
	spokenJSON, err := json.Marshal(spoken)
	if err != nil {
		return fmt.Errorf("encode spoken languages: %w", err)
	}
	screenJSON, err := json.Marshal(screen)
	if err != nil {
		return fmt.Errorf("encode screen languages: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
UPDATE games
SET spoken_langs = ?, screen_langs = ?, langs_checked_at = CURRENT_TIMESTAMP
WHERE id = ?`, string(spokenJSON), string(screenJSON), id); err != nil {
		return fmt.Errorf("update languages for %s: %w", id, err)
	}
	return nil
}

func (r *Repository) SetSourceGenres(ctx context.Context, gameID, source string, genres []domain.SourceGenre) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s genre replacement for %s: %w", source, gameID, err)
	}
	defer tx.Rollback()
	if err := setSourceGenres(ctx, tx, gameID, source, genres); err != nil {
		return fmt.Errorf("replace %s genres for %s: %w", source, gameID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s genre replacement for %s: %w", source, gameID, err)
	}
	return nil
}

func setSourceGenres(ctx context.Context, db dbHandle, gameID, source string, genres []domain.SourceGenre) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return fmt.Errorf("genre source is required")
	}
	if _, err := db.ExecContext(ctx,
		`DELETE FROM game_source_genres WHERE game_id = ? AND source = ?`,
		gameID,
		source,
	); err != nil {
		return err
	}
	stmt, err := db.PrepareContext(ctx, `
INSERT OR IGNORE INTO game_source_genres (game_id, source, genre, source_genre_id)
VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, sourceGenre := range genres {
		genre := strings.TrimSpace(sourceGenre.Name)
		if genre == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, gameID, source, genre, pointerValue(sourceGenre.SourceID)); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) UpdateHLTB(ctx context.Context, id string, update domain.HLTBUpdate) error {
	return r.updateScores(ctx, id, `
UPDATE games
SET hltb_main_extra = ?, hltb_rating = ?, hltb_id = ?, hltb_url = ?,
    hltb_checked_at = CURRENT_TIMESTAMP
WHERE id = ?`,
		pointerValue(update.MainExtraSeconds),
		pointerValue(update.Rating),
		pointerValue(update.ID),
		pointerValue(update.PageURL),
		id,
	)
}

func (r *Repository) UpdateMetacritic(ctx context.Context, id string, update domain.MetacriticUpdate) error {
	return r.updateScores(ctx, id, `
UPDATE games
SET metacritic_score = ?, metacritic_url = ?, metacritic_user_score = ?,
    metacritic_user_count = ?, mc_checked_at = CURRENT_TIMESTAMP
WHERE id = ?`,
		pointerValue(update.Critic),
		pointerValue(update.PageURL),
		pointerValue(update.User),
		pointerValue(update.UserCount),
		id,
	)
}

func (r *Repository) UpdateMetacriticPageURL(ctx context.Context, id string, pageURL *string) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE games SET metacritic_url = ? WHERE id = ?`,
		pointerValue(pageURL),
		id,
	); err != nil {
		return fmt.Errorf("update Metacritic URL for %s: %w", id, err)
	}
	return nil
}

func (r *Repository) UpdateOpenCritic(ctx context.Context, id string, update domain.OpenCriticUpdate) error {
	return r.updateScores(ctx, id, `
UPDATE games
SET opencritic_score = ?, opencritic_url = ?, opencritic_id = ?,
    opencritic_player_score = ?, opencritic_player_count = ?,
    oc_checked_at = CURRENT_TIMESTAMP
WHERE id = ?`,
		pointerValue(update.Critic),
		pointerValue(update.PageURL),
		pointerValue(update.ID),
		pointerValue(update.Player),
		pointerValue(update.PlayerCount),
		id,
	)
}

func (r *Repository) updateScores(ctx context.Context, id, query string, args ...any) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin score update for %s: %w", id, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("update scores for %s: %w", id, err)
	}
	if err := recomputeAverages(ctx, tx, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit score update for %s: %w", id, err)
	}
	return nil
}

func (r *Repository) ResetMissingChecks(ctx context.Context) (domain.ResetMissingResult, error) {
	var result domain.ResetMissingResult
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin reset missing checks: %w", err)
	}
	defer tx.Rollback()

	mc, err := tx.ExecContext(ctx, `UPDATE games SET mc_checked_at = NULL WHERE metacritic_score IS NULL`)
	if err != nil {
		return result, fmt.Errorf("reset Metacritic checks: %w", err)
	}
	oc, err := tx.ExecContext(ctx, `UPDATE games SET oc_checked_at = NULL WHERE opencritic_score IS NULL`)
	if err != nil {
		return result, fmt.Errorf("reset OpenCritic checks: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE games
SET hltb_checked_at = NULL
WHERE hltb_main_extra IS NULL AND hltb_rating IS NULL`); err != nil {
		return result, fmt.Errorf("reset HLTB checks: %w", err)
	}
	result.Metacritic, err = mc.RowsAffected()
	if err != nil {
		return result, fmt.Errorf("получить число сброшенных отметок Metacritic: %w", err)
	}
	result.OpenCritic, err = oc.RowsAffected()
	if err != nil {
		return result, fmt.Errorf("получить число сброшенных отметок OpenCritic: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit reset missing checks: %w", err)
	}
	return result, nil
}

// openCriticPlayerWeightExpr задаёт вес пользовательской оценки OpenCritic.
// При двух других источниках вес 0.5 даёт долю 20%: w / (1 + 1 + w) = 0.20.
const openCriticPlayerWeightExpr = `CASE
  WHEN COALESCE(opencritic_player_score,0) <= 0 OR COALESCE(opencritic_player_count,0) < 20 THEN 0.0
  WHEN COALESCE(opencritic_player_count,0) > 100 THEN 1.0
  ELSE 0.5
END`

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

func recomputeAverages(ctx context.Context, db dbHandle, id string) error {
	if _, err := db.ExecContext(ctx, `
UPDATE games
SET average_score = (`+averageExpr+`),
    critic_average_score = (`+criticAverageExpr+`),
    player_average_score = (`+playerAverageExpr+`)
WHERE id = ?`, id); err != nil {
		return fmt.Errorf("recompute averages for %s: %w", id, err)
	}
	return nil
}

func (r *Repository) RecomputeAllAverages(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, `
UPDATE games
SET average_score = (`+averageExpr+`),
    critic_average_score = (`+criticAverageExpr+`),
    player_average_score = (`+playerAverageExpr+`)`); err != nil {
		return fmt.Errorf("recompute all averages: %w", err)
	}
	return nil
}

func setGenres(ctx context.Context, db dbHandle, gameID string, genres []string) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM game_genres WHERE game_id = ?`, gameID); err != nil {
		return err
	}
	stmt, err := db.PrepareContext(ctx, `INSERT OR IGNORE INTO game_genres (game_id, genre) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, genre := range genres {
		genre = strings.TrimSpace(genre)
		if genre == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, gameID, genre); err != nil {
			return err
		}
	}
	return nil
}

func pointerValue[T any](value *T) any {
	if value == nil {
		return nil
	}
	return *value
}
