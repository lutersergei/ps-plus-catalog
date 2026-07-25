package store

import (
	"database/sql"
	"fmt"
	"time"
)

// AnnouncementGameRow — одна игра из сохранённого официального анонса.
type AnnouncementGameRow struct {
	Title   string
	AddedOn time.Time
}

// AnnouncementRow — разобранный анонс для атомарной записи в кэш.
type AnnouncementRow struct {
	URL           string
	LastModified  string
	ParserVersion int
	PublishedOn   time.Time
	Games         []AnnouncementGameRow
}

// AnnouncementVersions возвращает версии уже разобранных страниц. Sitemap
// lastmod используется как дешёвый ключ инвалидации кэша.
type AnnouncementCacheVersion struct {
	LastModified  string
	ParserVersion int
}

func AnnouncementVersions(db *sql.DB) (map[string]AnnouncementCacheVersion, error) {
	rows, err := db.Query(`SELECT url, last_modified, parser_version FROM catalog_announcements`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]AnnouncementCacheVersion)
	for rows.Next() {
		var url, lastModified string
		var parserVersion int
		if err := rows.Scan(&url, &lastModified, &parserVersion); err != nil {
			return nil, err
		}
		out[url] = AnnouncementCacheVersion{LastModified: lastModified, ParserVersion: parserVersion}
	}
	return out, rows.Err()
}

// ReplaceAnnouncement заменяет кэш одной страницы одной транзакцией.
func ReplaceAnnouncement(db *sql.DB, announcement AnnouncementRow) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
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
		return err
	}
	if _, err := tx.Exec(`DELETE FROM catalog_announcement_games WHERE announcement_url = ?`, announcement.URL); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
INSERT INTO catalog_announcement_games (announcement_url, game_title, added_on)
VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, game := range announcement.Games {
		if _, err := stmt.Exec(
			announcement.URL,
			game.Title,
			game.AddedOn.UTC().Format("2006-01-02"),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CatalogDateCandidate — дата и источник, извлечённые из кэша анонсов.
type CatalogDateCandidate struct {
	Title     string
	AddedOn   time.Time
	SourceURL string
}

type catalogDateQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// CatalogDateCandidates возвращает все пары названия и даты из кэша официальных
// анонсов; хронологический порядок делает дальнейшую обработку детерминированной.
func CatalogDateCandidates(db catalogDateQuerier) ([]CatalogDateCandidate, error) {
	rows, err := db.Query(`
SELECT game_title, added_on, announcement_url
FROM catalog_announcement_games
ORDER BY added_on, game_title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CatalogDateCandidate
	for rows.Next() {
		var c CatalogDateCandidate
		if err := rows.Scan(&c.Title, &c.AddedOn, &c.SourceURL); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CatalogDateTarget — текущий открытый период игры, которому можно уточнить
// дату по официальному анонсу.
type CatalogDateTarget struct {
	MembershipID      int64
	GameID            string
	Title             string
	TitleEn           string
	FirstSeenAt       time.Time
	AddedOn           sql.NullTime
	AddedOnSource     string
	PreviousRemovedOn sql.NullTime
	Initial           bool
}

// CurrentCatalogDateTargets возвращает активные игры с открытым периодом
// присутствия. Исторические закрытые периоды намеренно не участвуют в матчинге.
func CurrentCatalogDateTargets(db catalogDateQuerier) ([]CatalogDateTarget, error) {
	rows, err := db.Query(`
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
		return nil, err
	}
	defer rows.Close()
	var out []CatalogDateTarget
	for rows.Next() {
		var target CatalogDateTarget
		var previousRemovedOn sql.NullString
		if err := rows.Scan(
			&target.MembershipID,
			&target.GameID,
			&target.Title,
			&target.TitleEn,
			&target.FirstSeenAt,
			&target.AddedOn,
			&target.AddedOnSource,
			&previousRemovedOn,
			&target.Initial,
		); err != nil {
			return nil, err
		}
		if previousRemovedOn.Valid {
			removedOn, err := time.Parse("2006-01-02", previousRemovedOn.String)
			if err != nil {
				return nil, fmt.Errorf("parse previous catalog removal date %q: %w", previousRemovedOn.String, err)
			}
			target.PreviousRemovedOn = sql.NullTime{Time: removedOn, Valid: true}
		}
		out = append(out, target)
	}
	return out, rows.Err()
}

// CatalogDateMatch привязывает проверенный анонс к конкретному периоду.
type CatalogDateMatch struct {
	MembershipID int64
	AddedOn      time.Time
	SourceURL    string
}

// ApplyCatalogDateChanges атомарно сбрасывает устаревшие ошибочные совпадения
// к безопасной observed-дате и записывает найденные официальные анонсы.
// Если один membership есть в обоих списках, match применяется после reset.
func ApplyCatalogDateChanges(db *sql.DB, matches []CatalogDateMatch, resetMembershipIDs []int64) (int64, error) {
	if len(matches) == 0 && len(resetMembershipIDs) == 0 {
		return 0, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	changed, err := ApplyCatalogDateChangesTx(tx, matches, resetMembershipIDs)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return changed, nil
}

// ApplyCatalogDateChangesTx применяет изменения внутри уже открытой
// транзакции. Вызывающий код отвечает за commit/rollback; это позволяет
// удерживать блокировку каталога от чтения targets до записи дат.
func ApplyCatalogDateChangesTx(tx *sql.Tx, matches []CatalogDateMatch, resetMembershipIDs []int64) (int64, error) {
	if len(matches) == 0 && len(resetMembershipIDs) == 0 {
		return 0, nil
	}
	return applyCatalogDateChanges(tx, matches, resetMembershipIDs)
}

func applyCatalogDateChanges(db dbHandle, matches []CatalogDateMatch, resetMembershipIDs []int64) (int64, error) {
	if len(matches) == 0 && len(resetMembershipIDs) == 0 {
		return 0, nil
	}

	var changed int64
	resetStmt, err := db.Prepare(`
UPDATE catalog_memberships
SET added_on = date(first_seen_at), added_on_source = 'observed', source_url = NULL
WHERE id = ? AND removed_on IS NULL
  AND (
    date(added_on) != date(first_seen_at)
    OR COALESCE(added_on_source, '') != 'observed'
    OR source_url IS NOT NULL
  )`)
	if err != nil {
		return 0, err
	}
	defer resetStmt.Close()
	for _, membershipID := range resetMembershipIDs {
		res, err := resetStmt.Exec(membershipID)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("reset rows affected: %w", err)
		}
		changed += n
	}

	stmt, err := db.Prepare(`
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
		return 0, err
	}
	defer stmt.Close()

	for _, match := range matches {
		addedOn := match.AddedOn.UTC().Format("2006-01-02")
		res, err := stmt.Exec(
			addedOn,
			match.SourceURL,
			match.MembershipID,
			addedOn,
			match.SourceURL,
		)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("rows affected: %w", err)
		}
		changed += n
	}
	return changed, nil
}
