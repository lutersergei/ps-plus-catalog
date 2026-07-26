package main

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/psstore"
	"github.com/lutersergei/ps-plus-catalog/internal/store"
)

func TestSyncCatalogDatesDownloadsCachesAndAppliesAnnouncement(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := store.UpsertGame(db, store.GameRow{
		ID: "g1", Title: "Cyberpunk 2077", TitleEn: "Cyberpunk 2077",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := store.RecordCatalogSnapshot(
		db,
		[]string{"g1"},
		time.Date(2025, 7, 10, 0, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	const articleURL = "https://blog.playstation.com/2025/07/09/playstation-plus-game-catalog-for-july-games/"
	sitemapIndex := `<?xml version="1.0"?><sitemapindex>
<sitemap><loc>https://blog.playstation.com/wp-sitemap-posts-recent.xml</loc></sitemap>
<sitemap><loc>https://blog.playstation.com/wp-sitemap-posts-2022.xml</loc></sitemap>
</sitemapindex>`
	sitemap := `<?xml version="1.0"?><urlset>
<url><loc>` + articleURL + `</loc><lastmod>2025-07-09T16:00:00Z</lastmod></url>
</urlset>`
	article := `<html><body>
<time class="entry-date published" datetime="2025-07-09T08:30:00-07:00"></time>
<div class="post-single__content single__content entry-content">
<p>Cyberpunk 2077 is available to play today, July 9. All other titles will be available on July 15.</p>
<h2>PlayStation Plus Extra and Premium | Game Catalog</h2>
<p><strong>Cyberpunk 2077 | PS5, PS4</strong></p>
<h2>PlayStation Plus Premium</h2>
<div class="post-single__footer"></div></div></body></html>`

	articleRequests := 0
	client := &http.Client{Transport: syncRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/wp-sitemap.xml":
			return syncTestResponse(http.StatusOK, sitemapIndex), nil
		case strings.Contains(req.URL.Path, "wp-sitemap-posts-"):
			return syncTestResponse(http.StatusOK, sitemap), nil
		case req.URL.String() == articleURL:
			articleRequests++
			return syncTestResponse(http.StatusOK, article), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL)
			return nil, nil
		}
	})}

	first, err := syncCatalogDates(context.Background(), db, client, false, 2022)
	if err != nil {
		t.Fatalf("first dates sync: %v", err)
	}
	if first.Downloaded != 1 || first.Matched != 1 || first.Updated != 1 {
		t.Fatalf("first stats=%+v", first)
	}
	if articleRequests != 1 {
		t.Fatalf("article requests=%d, want 1", articleRequests)
	}

	var addedOn, source, sourceURL string
	if err := db.QueryRow(`
SELECT date(added_on), added_on_source, source_url
FROM catalog_memberships WHERE game_id = 'g1' AND removed_on IS NULL`,
	).Scan(&addedOn, &source, &sourceURL); err != nil {
		t.Fatalf("read date: %v", err)
	}
	if addedOn != "2025-07-09" || source != "announcement" || sourceURL != articleURL {
		t.Fatalf("added=%q source=%q url=%q", addedOn, source, sourceURL)
	}

	second, err := syncCatalogDates(context.Background(), db, client, false, 2022)
	if err != nil {
		t.Fatalf("second dates sync: %v", err)
	}
	if second.Downloaded != 0 || second.Cached != 1 || second.Updated != 0 || articleRequests != 1 {
		t.Fatalf("second stats=%+v article requests=%d", second, articleRequests)
	}
}

func TestSyncCatalogDatesAppliesCompleteVerifiedBackfill(t *testing.T) {
	manifest, err := loadCatalogDateBackfill()
	if err != nil {
		t.Fatalf("load backfill: %v", err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ids := make([]string, 0, len(manifest.Entries)+len(manifest.KeepNull))
	for _, entry := range manifest.Entries {
		if err := store.UpsertGame(db, store.GameRow{ID: entry.GameID, Title: entry.Title, TitleEn: entry.Title}); err != nil {
			t.Fatalf("upsert %q: %v", entry.GameID, err)
		}
		ids = append(ids, entry.GameID)
	}
	for _, identity := range manifest.KeepNull {
		if err := store.UpsertGame(db, store.GameRow{ID: identity.GameID, Title: identity.Title, TitleEn: identity.Title}); err != nil {
			t.Fatalf("upsert %q: %v", identity.GameID, err)
		}
		ids = append(ids, identity.GameID)
	}
	if _, err := store.RecordCatalogSnapshot(
		db,
		ids,
		time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	const sitemapIndex = `<?xml version="1.0"?><sitemapindex>
<sitemap><loc>https://blog.playstation.com/wp-sitemap-posts-recent.xml</loc></sitemap>
</sitemapindex>`
	const emptySitemap = `<?xml version="1.0"?><urlset></urlset>`
	client := &http.Client{Transport: syncRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/wp-sitemap.xml":
			return syncTestResponse(http.StatusOK, sitemapIndex), nil
		case "/wp-sitemap-posts-recent.xml":
			return syncTestResponse(http.StatusOK, emptySitemap), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL)
			return nil, nil
		}
	})}

	stats, err := syncCatalogDates(context.Background(), db, client, false, 2026)
	if err != nil {
		t.Fatalf("sync dates: %v", err)
	}
	if stats.Targets != 161 || stats.Verified != 160 || stats.Matched != 160 ||
		stats.KeptNull != 1 || stats.Unmatched != 1 || stats.Updated != 160 {
		t.Fatalf("stats=%+v", stats)
	}
	var active, undated, verified, launchVerified int
	if err := db.QueryRow(`
SELECT COUNT(*),
       SUM(cm.added_on IS NULL),
       SUM(cm.added_on_source = 'verified'),
       SUM(date(cm.added_on) = '2022-06-23' AND cm.added_on_source = 'verified')
FROM games g
JOIN catalog_memberships cm ON cm.game_id = g.id AND cm.removed_on IS NULL
WHERE g.active = 1`).Scan(&active, &undated, &verified, &launchVerified); err != nil {
		t.Fatalf("read backfill totals: %v", err)
	}
	if active != 161 || undated != 1 || verified != 160 || launchVerified != 130 {
		t.Fatalf("active=%d undated=%d verified=%d launch=%d", active, undated, verified, launchVerified)
	}
	var forTheKingDate sql.NullTime
	if err := db.QueryRow(`
SELECT cm.added_on
FROM catalog_memberships cm
WHERE cm.game_id = 'EP4395-CUSA12941_00-FORTHEKING000001'
  AND cm.removed_on IS NULL`).Scan(&forTheKingDate); err != nil {
		t.Fatalf("read For The King: %v", err)
	}
	if forTheKingDate.Valid {
		t.Fatalf("For The King date=%v, want NULL", forTheKingDate.Time)
	}

	second, err := syncCatalogDates(context.Background(), db, client, false, 2026)
	if err != nil {
		t.Fatalf("second sync dates: %v", err)
	}
	if second.Updated != 0 || second.Verified != 160 || second.KeptNull != 1 {
		t.Fatalf("second stats=%+v", second)
	}
}

func TestMatchCatalogDateTargetsIgnoresCandidatesTooFarAfterObservedFirstSeen(t *testing.T) {
	firstSeen := time.Date(2025, 7, 10, 0, 0, 0, 0, time.UTC)
	result := matchCatalogDateTargets(
		[]store.CatalogDateTarget{{
			MembershipID: 1,
			Title:        "Cyberpunk 2077",
			TitleEn:      "Cyberpunk 2077",
			FirstSeenAt:  firstSeen,
		}},
		[]store.CatalogDateCandidate{{
			Title: "Cyberpunk 2077", AddedOn: firstSeen.AddDate(0, 0, 46), SourceURL: "https://example.com/future",
		}},
	)
	if result.Matched != 0 || len(result.Matches) != 0 || len(result.UnmatchedGames) != 1 {
		t.Fatalf("result=%+v", result)
	}
}

// TestMatchCatalogDateTargetsIgnoresFirstSeenForInitialMembership проверяет
// бутстрап: first_seen — дата запуска сервиса, а не добавления в PS Plus.
// Исторический анонс 2022 года должен матчиться на период без observed-даты.
func TestMatchCatalogDateTargetsIgnoresFirstSeenForInitialMembership(t *testing.T) {
	serviceStart := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	const sourceURL = "https://example.com/2022"
	result := matchCatalogDateTargets(
		[]store.CatalogDateTarget{{
			MembershipID: 1,
			Title:        "Horizon Forbidden West",
			TitleEn:      "Horizon Forbidden West",
			FirstSeenAt:  serviceStart,
			Initial:      true,
		}},
		[]store.CatalogDateCandidate{{
			Title:     "Horizon Forbidden West",
			AddedOn:   time.Date(2022, 6, 23, 0, 0, 0, 0, time.UTC),
			SourceURL: sourceURL,
		}},
	)
	if result.Matched != 1 || len(result.Matches) != 1 {
		t.Fatalf("исторический анонс должен матчиться на initial-период: %+v", result)
	}
	if got := result.Matches[0].SourceURL; got != sourceURL {
		t.Fatalf("source URL=%q, want %q", got, sourceURL)
	}
}

// TestRefreshCatalogAnnouncementCacheStopsOnCancelledContext проверяет, что
// при уже отменённом контексте цикл не делает сетевых запросов и не засчитывает
// их как ошибки разбора.
func TestRefreshCatalogAnnouncementCacheStopsOnCancelledContext(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	requests := 0
	client := &http.Client{Transport: syncRoundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return syncTestResponse(http.StatusOK, "<html></html>"), nil
	})}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stats, err := refreshCatalogAnnouncementCache(ctx, db, client, []psstore.AnnouncementRef{
		{URL: "https://blog.playstation.com/example", LastModified: "2026-07-21T00:00:00Z"},
	}, false)
	if err != context.Canceled {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if requests != 0 {
		t.Fatalf("network requests=%d, want 0", requests)
	}
	if stats.ParseErrors != 0 || stats.Downloaded != 0 || stats.Cached != 0 {
		t.Fatalf("stats=%+v, want zeroed", stats)
	}
}

// TestRefreshCatalogAnnouncementCacheStopsWhenContextCancelledDuringFetch
// проверяет, что отмена во время HTTP-запроса возвращает ошибку контекста,
// а не считается ParseError (на единственном/последнем ref это важно).
func TestRefreshCatalogAnnouncementCacheStopsWhenContextCancelledDuringFetch(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Transport: syncRoundTripFunc(func(*http.Request) (*http.Response, error) {
		cancel()
		return nil, context.Canceled
	})}

	stats, err := refreshCatalogAnnouncementCache(ctx, db, client, []psstore.AnnouncementRef{
		{URL: "https://blog.playstation.com/only", LastModified: "2026-07-21T00:00:00Z"},
	}, false)
	if err != context.Canceled {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if stats.ParseErrors != 0 {
		t.Fatalf("ParseErrors=%d, want 0 — отмена не ошибка разбора", stats.ParseErrors)
	}
}

// TestMatchCatalogDateTargetsIgnoresStaleAnnouncementForReturningGame
// проверяет, что устаревший анонс предыдущего появления не применяется к
// observed-периоду возврата: окно дат вокруг first_seen отсекает его.
func TestMatchCatalogDateTargetsIgnoresStaleAnnouncementForReturningGame(t *testing.T) {
	firstSeen := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	result := matchCatalogDateTargets(
		[]store.CatalogDateTarget{{
			MembershipID: 1,
			Title:        "Far Cry 6",
			TitleEn:      "Far Cry 6",
			FirstSeenAt:  firstSeen,
			AddedOn: sql.NullTime{
				Time:  time.Date(2023, 1, 17, 0, 0, 0, 0, time.UTC),
				Valid: true,
			},
			AddedOnSource: "announcement",
			PreviousRemovedOn: sql.NullTime{
				Time:  time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
				Valid: true,
			},
		}},
		[]store.CatalogDateCandidate{{
			Title: "Far Cry 6", AddedOn: time.Date(2023, 1, 17, 0, 0, 0, 0, time.UTC), SourceURL: "https://example.com/old",
		}},
	)
	if result.Matched != 0 || len(result.Matches) != 0 || len(result.UnmatchedGames) != 1 {
		t.Fatalf("устаревший анонс не должен матчиться на вернувшуюся игру: %+v", result)
	}
	if len(result.ResetMembershipIDs) != 1 || result.ResetMembershipIDs[0] != 1 {
		t.Fatalf("устаревшая записанная дата должна быть сброшена: %+v", result)
	}
}

// TestMatchCatalogDateTargetsAcceptsAnnouncementWithinWindowBeforeObserved
// проверяет, что для observed-периода анонс в пределах окна до first_seen
// принимается (анонс чуть раньше появления в endpoint — нормальный случай).
func TestMatchCatalogDateTargetsAcceptsAnnouncementWithinWindowBeforeObserved(t *testing.T) {
	firstSeen := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	const sourceURL = "https://example.com/announcement"
	result := matchCatalogDateTargets(
		[]store.CatalogDateTarget{{
			MembershipID: 1,
			Title:        "Far Cry 6",
			TitleEn:      "Far Cry 6",
			FirstSeenAt:  firstSeen,
		}},
		[]store.CatalogDateCandidate{{
			Title: "Far Cry 6", AddedOn: firstSeen.AddDate(0, 0, -10), SourceURL: sourceURL,
		}},
	)
	if result.Matched != 1 || len(result.Matches) != 1 {
		t.Fatalf("анонс в пределах окна должен матчиться: %+v", result)
	}
	if got := result.Matches[0].SourceURL; got != sourceURL {
		t.Fatalf("source URL=%q, want %q", got, sourceURL)
	}
}

// TestMatchCatalogDateTargetsUsesCalendarDaysForObservedWindowBounds проверяет,
// что окно ±45 для observed считается по UTC-дням.
func TestMatchCatalogDateTargetsUsesCalendarDaysForObservedWindowBounds(t *testing.T) {
	firstSeen := time.Date(2026, 7, 21, 15, 30, 0, 0, time.UTC)
	boundary := utcDate(firstSeen).AddDate(0, 0, -catalogDateMatchWindowDays)
	const sourceURL = "https://example.com/boundary"
	result := matchCatalogDateTargets(
		[]store.CatalogDateTarget{{
			MembershipID: 1,
			Title:        "Far Cry 6",
			TitleEn:      "Far Cry 6",
			FirstSeenAt:  firstSeen,
		}},
		[]store.CatalogDateCandidate{{
			Title: "Far Cry 6", AddedOn: boundary, SourceURL: sourceURL,
		}},
	)
	if result.Matched != 1 || len(result.Matches) != 1 {
		t.Fatalf("граница ±%d календарных дней должна включать анонс: %+v", catalogDateMatchWindowDays, result)
	}
}
