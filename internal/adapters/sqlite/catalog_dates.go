package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

func (r *Repository) AnnouncementVersions(ctx context.Context) (map[string]domain.AnnouncementCacheVersion, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT url, last_modified, parser_version
FROM catalog_announcements`)
	if err != nil {
		return nil, fmt.Errorf("query announcement versions: %w", err)
	}
	defer rows.Close()

	versions := make(map[string]domain.AnnouncementCacheVersion)
	for rows.Next() {
		var rawURL string
		var version domain.AnnouncementCacheVersion
		if err := rows.Scan(&rawURL, &version.LastModified, &version.ParserVersion); err != nil {
			return nil, fmt.Errorf("scan announcement version: %w", err)
		}
		versions[rawURL] = version
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query announcement versions: %w", err)
	}
	return versions, nil
}

// ReplaceAnnouncement атомарно заменяет одну запись кэша разобранного анонса.
func (r *Repository) ReplaceAnnouncement(ctx context.Context, announcement domain.CachedAnnouncement) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin announcement replacement: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO catalog_announcements (url, last_modified, parser_version, published_on, fetched_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(url) DO UPDATE SET
  last_modified = excluded.last_modified,
  parser_version = excluded.parser_version,
  published_on = excluded.published_on,
  fetched_at = CURRENT_TIMESTAMP`,
		announcement.URL,
		announcement.LastModified,
		announcement.ParserVersion,
		announcement.PublishedOn.UTC().Format("2006-01-02"),
	); err != nil {
		return fmt.Errorf("upsert announcement %s: %w", announcement.URL, err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM catalog_announcement_games WHERE announcement_url = ?`,
		announcement.URL,
	); err != nil {
		return fmt.Errorf("clear announcement games %s: %w", announcement.URL, err)
	}
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO catalog_announcement_games (announcement_url, game_title, added_on)
VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare announcement games: %w", err)
	}
	defer stmt.Close()
	for _, game := range announcement.Games {
		if _, err := stmt.ExecContext(
			ctx,
			announcement.URL,
			game.Title,
			game.AddedOn.UTC().Format("2006-01-02"),
		); err != nil {
			return fmt.Errorf("insert announcement game %q: %w", game.Title, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit announcement replacement: %w", err)
	}
	return nil
}

// ReconcileCatalogDates удерживает блокировку синхронизации, загружает текущее
// состояние, передаёт его сервису для чистого расчёта плана и применяет план в
// той же транзакции. Детали транзакции остаются внутри SQLite-адаптера.
func (r *Repository) ReconcileCatalogDates(
	ctx context.Context,
	planner func([]domain.CatalogDateTarget, []domain.CatalogDateCandidate) (domain.CatalogDatePlan, error),
) (domain.CatalogDateApplyResult, error) {
	var result domain.CatalogDateApplyResult
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin catalog date reconciliation: %w", err)
	}
	defer tx.Rollback()
	if err := acquireCatalogSyncLock(ctx, tx); err != nil {
		return result, err
	}

	candidates, err := catalogDateCandidates(ctx, tx)
	if err != nil {
		return result, err
	}
	targets, err := currentCatalogDateTargets(ctx, tx)
	if err != nil {
		return result, err
	}
	result.Candidates = len(candidates)
	result.Targets = len(targets)

	plan, err := planner(targets, candidates)
	if err != nil {
		return result, err
	}
	backfillUpdated, err := applyCatalogDateBackfill(
		ctx,
		tx,
		plan.BackfillMatches,
		plan.KeepNullIDs,
	)
	if err != nil {
		return result, err
	}
	announcementUpdated, err := applyCatalogDateChanges(
		ctx,
		tx,
		plan.AnnouncementMatches,
		plan.ResetMembershipIDs,
	)
	if err != nil {
		return result, err
	}
	result.Updated = backfillUpdated + announcementUpdated
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit catalog date reconciliation: %w", err)
	}
	return result, nil
}

func catalogDateCandidates(ctx context.Context, tx *sql.Tx) ([]domain.CatalogDateCandidate, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT game_title, added_on, announcement_url
FROM catalog_announcement_games
ORDER BY added_on, game_title`)
	if err != nil {
		return nil, fmt.Errorf("query catalog date candidates: %w", err)
	}
	defer rows.Close()

	var candidates []domain.CatalogDateCandidate
	for rows.Next() {
		var candidate domain.CatalogDateCandidate
		if err := rows.Scan(&candidate.Title, &candidate.AddedOn, &candidate.SourceURL); err != nil {
			return nil, fmt.Errorf("scan catalog date candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query catalog date candidates: %w", err)
	}
	return candidates, nil
}

func currentCatalogDateTargets(ctx context.Context, tx *sql.Tx) ([]domain.CatalogDateTarget, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT cm.id, g.id, g.title, COALESCE(g.title_en, g.title), cm.first_seen_at,
       cm.added_on, COALESCE(cm.added_on_source, ''),
       (
         SELECT MAX(previous.removed_on)
         FROM catalog_memberships previous
         WHERE previous.game_id = cm.game_id
           AND previous.id != cm.id
           AND previous.removed_on IS NOT NULL
           AND previous.first_seen_at < cm.first_seen_at
       ),
       cm.first_seen_at = (SELECT MIN(first_seen_at) FROM catalog_memberships)
FROM catalog_memberships cm
JOIN games g ON g.id = cm.game_id
WHERE cm.removed_on IS NULL AND g.active = 1
ORDER BY g.title`)
	if err != nil {
		return nil, fmt.Errorf("query catalog date targets: %w", err)
	}
	defer rows.Close()

	var targets []domain.CatalogDateTarget
	for rows.Next() {
		var target domain.CatalogDateTarget
		var addedOn sql.NullTime
		var previousRemovedOn sql.NullString
		if err := rows.Scan(
			&target.MembershipID,
			&target.GameID,
			&target.Title,
			&target.TitleEn,
			&target.FirstSeenAt,
			&addedOn,
			&target.AddedOnSource,
			&previousRemovedOn,
			&target.Initial,
		); err != nil {
			return nil, fmt.Errorf("scan catalog date target: %w", err)
		}
		if addedOn.Valid {
			value := addedOn.Time
			target.AddedOn = &value
		}
		if previousRemovedOn.Valid {
			value, err := time.Parse("2006-01-02", previousRemovedOn.String)
			if err != nil {
				return nil, fmt.Errorf("parse previous removal date %q: %w", previousRemovedOn.String, err)
			}
			target.PreviousRemovedOn = &value
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query catalog date targets: %w", err)
	}
	return targets, nil
}

func applyCatalogDateBackfill(
	ctx context.Context,
	tx *sql.Tx,
	matches []domain.CatalogDateBackfillMatch,
	keepNullMembershipIDs []int64,
) (int64, error) {
	if len(matches) == 0 && len(keepNullMembershipIDs) == 0 {
		return 0, nil
	}
	if err := validateCatalogDateBackfillChanges(matches, keepNullMembershipIDs); err != nil {
		return 0, err
	}

	var changed int64
	clearStmt, err := tx.PrepareContext(ctx, `
UPDATE catalog_memberships
SET added_on = NULL, added_on_source = NULL, source_url = NULL
WHERE id = ? AND removed_on IS NULL
  AND (added_on IS NOT NULL OR added_on_source IS NOT NULL OR source_url IS NOT NULL)`)
	if err != nil {
		return 0, fmt.Errorf("prepare catalog date clear: %w", err)
	}
	defer clearStmt.Close()
	for _, membershipID := range keepNullMembershipIDs {
		res, err := clearStmt.ExecContext(ctx, membershipID)
		if err != nil {
			return 0, fmt.Errorf("clear catalog date %d: %w", membershipID, err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("clear catalog date rows affected: %w", err)
		}
		changed += rows
	}

	stmt, err := tx.PrepareContext(ctx, `
UPDATE catalog_memberships
SET added_on = ?, added_on_source = 'verified', source_url = ?
WHERE id = ? AND removed_on IS NULL
  AND (
    added_on IS NULL
    OR date(added_on) != ?
    OR COALESCE(added_on_source, '') != 'verified'
    OR COALESCE(source_url, '') != ?
  )`)
	if err != nil {
		return 0, fmt.Errorf("prepare verified catalog dates: %w", err)
	}
	defer stmt.Close()
	for _, match := range matches {
		addedOn := match.AddedOn.UTC().Format("2006-01-02")
		res, err := stmt.ExecContext(
			ctx,
			addedOn,
			match.SourceURL,
			match.MembershipID,
			addedOn,
			match.SourceURL,
		)
		if err != nil {
			return 0, fmt.Errorf("apply verified catalog date %d: %w", match.MembershipID, err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("verified catalog date rows affected: %w", err)
		}
		changed += rows
	}
	return changed, nil
}

func validateCatalogDateBackfillChanges(
	matches []domain.CatalogDateBackfillMatch,
	keepNullMembershipIDs []int64,
) error {
	seen := make(map[int64]string, len(matches)+len(keepNullMembershipIDs))
	for _, membershipID := range keepNullMembershipIDs {
		if membershipID <= 0 {
			return fmt.Errorf("invalid keep-null catalog membership id %d", membershipID)
		}
		if action, exists := seen[membershipID]; exists {
			return fmt.Errorf("duplicate catalog membership id %d for keep-null and %s", membershipID, action)
		}
		seen[membershipID] = "keep-null"
	}
	for _, match := range matches {
		if match.MembershipID <= 0 {
			return fmt.Errorf("invalid verified catalog membership id %d", match.MembershipID)
		}
		if match.AddedOn.IsZero() {
			return fmt.Errorf("verified catalog membership %d has an empty date", match.MembershipID)
		}
		if match.SourceURL == "" {
			return fmt.Errorf("verified catalog membership %d has an empty source URL", match.MembershipID)
		}
		if action, exists := seen[match.MembershipID]; exists {
			return fmt.Errorf("duplicate catalog membership id %d for verified and %s", match.MembershipID, action)
		}
		seen[match.MembershipID] = "verified"
	}
	return nil
}

func applyCatalogDateChanges(
	ctx context.Context,
	tx *sql.Tx,
	matches []domain.CatalogDateMatch,
	resetMembershipIDs []int64,
) (int64, error) {
	if len(matches) == 0 && len(resetMembershipIDs) == 0 {
		return 0, nil
	}

	var changed int64
	resetStmt, err := tx.PrepareContext(ctx, `
UPDATE catalog_memberships
SET added_on = date(first_seen_at), added_on_source = 'observed', source_url = NULL
WHERE id = ? AND removed_on IS NULL
  AND (
    date(added_on) != date(first_seen_at)
    OR COALESCE(added_on_source, '') != 'observed'
    OR source_url IS NOT NULL
  )`)
	if err != nil {
		return 0, fmt.Errorf("prepare observed catalog date reset: %w", err)
	}
	defer resetStmt.Close()
	for _, membershipID := range resetMembershipIDs {
		res, err := resetStmt.ExecContext(ctx, membershipID)
		if err != nil {
			return 0, fmt.Errorf("reset catalog date %d: %w", membershipID, err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("reset catalog date rows affected: %w", err)
		}
		changed += rows
	}

	stmt, err := tx.PrepareContext(ctx, `
UPDATE catalog_memberships
SET added_on = ?, added_on_source = 'announcement', source_url = ?
WHERE id = ?
  AND removed_on IS NULL
  AND (
    added_on IS NULL
    OR date(added_on) != ?
    OR COALESCE(added_on_source, '') != 'announcement'
    OR COALESCE(source_url, '') != ?
  )`)
	if err != nil {
		return 0, fmt.Errorf("prepare announcement catalog dates: %w", err)
	}
	defer stmt.Close()
	for _, match := range matches {
		addedOn := match.AddedOn.UTC().Format("2006-01-02")
		res, err := stmt.ExecContext(
			ctx,
			addedOn,
			match.SourceURL,
			match.MembershipID,
			addedOn,
			match.SourceURL,
		)
		if err != nil {
			return 0, fmt.Errorf("apply announcement catalog date %d: %w", match.MembershipID, err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("announcement catalog date rows affected: %w", err)
		}
		changed += rows
	}
	return changed, nil
}
