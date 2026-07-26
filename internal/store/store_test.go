package store

import (
	"database/sql"
	"errors"
	"math"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// newTestDB открывает временную БД и наполняет её n играми (id g1..gN, active=1).
func newTestDB(t *testing.T, n int) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		id := "g" + strconv.Itoa(i)
		if err := UpsertGame(tx, GameRow{ID: id, Title: "Game " + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSQLiteDSNAddsDefaultPragmas(t *testing.T) {
	const wantPragmas = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"

	if got, want := sqliteDSN("ps-extra.db"), "ps-extra.db?"+wantPragmas; got != want {
		t.Fatalf("sqliteDSN plain path = %q, ждали %q", got, want)
	}
	if got, want := sqliteDSN("file:ps-extra.db?cache=shared"), "file:ps-extra.db?cache=shared&"+wantPragmas; got != want {
		t.Fatalf("sqliteDSN existing query = %q, ждали %q", got, want)
	}
}

func TestOpenAppliesSQLitePragmas(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	var journalMode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode=%q, ждали wal", journalMode)
	}

	var busyTimeout int
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout=%d, ждали 5000", busyTimeout)
	}

	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys=%d, ждали 1", foreignKeys)
	}
}

func TestListGames_PageClamp(t *testing.T) {
	db := newTestDB(t, 30) // 30 игр, pageSize 24 → 2 страницы
	res, err := ListGames(db, ListParams{Page: 9223372036854775807, PageSize: 24})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res.TotalPages != 2 {
		t.Fatalf("ждали 2 страницы, получили %d", res.TotalPages)
	}
	// Огромный page кламп­ится к последней странице, а не схлопывается в пустоту
	// из-за переполнения OFFSET.
	if res.Page != 2 {
		t.Errorf("page: ждали 2 (кламп), получили %d", res.Page)
	}
	if len(res.Games) != 6 { // 30 - 24
		t.Errorf("на 2-й странице ждали 6 игр, получили %d", len(res.Games))
	}
}

func TestNormalizeParams(t *testing.T) {
	p := ListParams{
		Page:     0,
		Search:   string(make([]byte, maxSearchLen+50)),
		Genres:   make([]string, maxGenres+10),
		YearFrom: 2020, YearTo: 2000, // перевёрнутый диапазон
		AvgFrom: 90, AvgTo: 50,
		CriticFrom: 90, CriticTo: 50,
		PlayerFrom: 90, PlayerTo: 50,
	}
	NormalizeParams(&p)
	if p.Page != 1 {
		t.Errorf("page<1 → 1, получили %d", p.Page)
	}
	if len(p.Search) != maxSearchLen {
		t.Errorf("длина поиска: ждали %d, получили %d", maxSearchLen, len(p.Search))
	}
	if len(p.Genres) != maxGenres {
		t.Errorf("число жанров: ждали %d, получили %d", maxGenres, len(p.Genres))
	}
	if p.YearFrom != 0 || p.YearTo != 0 {
		t.Errorf("перевёрнутый диапазон годов должен обнулиться, получили %d..%d", p.YearFrom, p.YearTo)
	}
	if p.AvgFrom != 0 || p.AvgTo != 0 {
		t.Errorf("перевёрнутый диапазон средней оценки должен обнулиться, получили %.1f..%.1f", p.AvgFrom, p.AvgTo)
	}
	if p.CriticFrom != 0 || p.CriticTo != 0 {
		t.Errorf("перевёрнутый диапазон критиков должен обнулиться, получили %.1f..%.1f", p.CriticFrom, p.CriticTo)
	}
	if p.PlayerFrom != 0 || p.PlayerTo != 0 {
		t.Errorf("перевёрнутый диапазон игроков должен обнулиться, получили %.1f..%.1f", p.PlayerFrom, p.PlayerTo)
	}
}

func TestDeactivateMissingAndCount(t *testing.T) {
	db := newTestDB(t, 5)
	if n, err := CountActive(db); err != nil || n != 5 {
		t.Fatalf("CountActive=%d err=%v, ждали 5", n, err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	// В снимок вошли только g1,g2,g3 → g4,g5 деактивируются.
	got, err := DeactivateMissing(tx, []string{"g1", "g2", "g3"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Errorf("деактивировано %d, ждали 2", got)
	}
	if n, _ := CountActive(db); n != 3 {
		t.Errorf("после деактивации активно %d, ждали 3", n)
	}
}

func TestRecordCatalogSnapshotTracksInitialChangesAndReturn(t *testing.T) {
	db := newTestDB(t, 2)
	first := time.Date(2026, 7, 20, 23, 55, 0, 0, time.UTC)

	initial, err := RecordCatalogSnapshot(db, []string{"g1", "g2"}, first)
	if err != nil {
		t.Fatalf("initial snapshot: %v", err)
	}
	if !initial.Initial || initial.Added != 2 || initial.Removed != 0 {
		t.Fatalf("initial=%+v, ждали Initial=true Added=2 Removed=0", initial)
	}

	var initialAdded sql.NullString
	if err := db.QueryRow(`
SELECT added_on FROM catalog_memberships
WHERE game_id = 'g1' AND removed_on IS NULL`).Scan(&initialAdded); err != nil {
		t.Fatalf("read initial membership: %v", err)
	}
	if initialAdded.Valid {
		t.Fatalf("первый снимок не должен придумывать дату добавления: %v", initialAdded)
	}

	if err := UpsertGame(db, GameRow{ID: "g3", Title: "Game 3"}); err != nil {
		t.Fatalf("seed g3: %v", err)
	}
	second := time.Date(2026, 7, 21, 1, 5, 0, 0, time.UTC)
	changed, err := RecordCatalogSnapshot(db, []string{"g2", "g3"}, second)
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if changed.Initial || changed.Added != 1 || changed.Removed != 1 {
		t.Fatalf("changed=%+v, ждали Initial=false Added=1 Removed=1", changed)
	}

	var g1Removed, g3Added, g3Source string
	if err := db.QueryRow(`
SELECT
  (SELECT date(removed_on) FROM catalog_memberships WHERE game_id = 'g1'),
  (SELECT date(added_on) FROM catalog_memberships WHERE game_id = 'g3'),
  (SELECT added_on_source FROM catalog_memberships WHERE game_id = 'g3')
`).Scan(&g1Removed, &g3Added, &g3Source); err != nil {
		t.Fatalf("read changes: %v", err)
	}
	if g1Removed != "2026-07-21" || g3Added != "2026-07-21" || g3Source != "observed" {
		t.Fatalf("g1 removed=%q, g3 added=%q source=%q", g1Removed, g3Added, g3Source)
	}

	third := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	returned, err := RecordCatalogSnapshot(db, []string{"g1", "g2", "g3"}, third)
	if err != nil {
		t.Fatalf("return snapshot: %v", err)
	}
	if returned.Added != 1 || returned.Removed != 0 {
		t.Fatalf("returned=%+v, ждали новый период только для g1", returned)
	}

	var g1Periods int
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_memberships WHERE game_id = 'g1'`).Scan(&g1Periods); err != nil {
		t.Fatalf("count g1 periods: %v", err)
	}
	if g1Periods != 2 {
		t.Fatalf("периодов g1=%d, ждали 2", g1Periods)
	}

	officialDate := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	const sourceURL = "https://blog.playstation.com/example/"
	if err := SetCatalogAddedDate(db, "g1", officialDate, "announcement", sourceURL); err != nil {
		t.Fatalf("set official date: %v", err)
	}
	var addedOn, source, url string
	if err := db.QueryRow(`
SELECT date(added_on), added_on_source, source_url
FROM catalog_memberships
WHERE game_id = 'g1' AND removed_on IS NULL`).Scan(&addedOn, &source, &url); err != nil {
		t.Fatalf("read official date: %v", err)
	}
	if addedOn != "2026-08-01" || source != "announcement" || url != sourceURL {
		t.Fatalf("added=%q source=%q url=%q", addedOn, source, url)
	}
}

func TestCurrentCatalogDateTargetsIdentifiesInitialAndReturningPeriods(t *testing.T) {
	db := newTestDB(t, 2)
	first := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	removed := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	returned := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)

	if _, err := RecordCatalogSnapshot(db, []string{"g1", "g2"}, first); err != nil {
		t.Fatalf("initial snapshot: %v", err)
	}
	if _, err := RecordCatalogSnapshot(db, []string{"g2"}, removed); err != nil {
		t.Fatalf("removal snapshot: %v", err)
	}
	if _, err := RecordCatalogSnapshot(db, []string{"g1", "g2"}, returned); err != nil {
		t.Fatalf("return snapshot: %v", err)
	}
	if err := SetCatalogAddedDate(
		db,
		"g1",
		time.Date(2025, 9, 16, 0, 0, 0, 0, time.UTC),
		"announcement",
		"https://example.com/stale",
	); err != nil {
		t.Fatalf("set stale date: %v", err)
	}

	targets, err := CurrentCatalogDateTargets(db)
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	byID := make(map[string]CatalogDateTarget, len(targets))
	for _, target := range targets {
		byID[target.GameID] = target
	}
	if !byID["g2"].Initial {
		t.Fatalf("g2 target=%+v, want initial period", byID["g2"])
	}
	g1 := byID["g1"]
	if g1.Initial || !g1.PreviousRemovedOn.Valid {
		t.Fatalf("g1 target=%+v, want returning period with previous removal", g1)
	}
	if got := g1.PreviousRemovedOn.Time.Format("2006-01-02"); got != "2026-07-10" {
		t.Fatalf("g1 previous removal=%s, want 2026-07-10", got)
	}
	changed, err := ApplyCatalogDateChanges(db, nil, []int64{g1.MembershipID})
	if err != nil {
		t.Fatalf("reset stale date: %v", err)
	}
	if changed != 1 {
		t.Fatalf("changed=%d, want 1", changed)
	}
	var addedOn, source string
	var sourceURL sql.NullString
	if err := db.QueryRow(`
SELECT date(added_on), added_on_source, source_url
FROM catalog_memberships
WHERE id = ?`, g1.MembershipID).Scan(&addedOn, &source, &sourceURL); err != nil {
		t.Fatalf("read reset membership: %v", err)
	}
	if addedOn != "2026-07-20" || source != "observed" || sourceURL.Valid {
		t.Fatalf("reset membership added=%q source=%q url=%v", addedOn, source, sourceURL)
	}
}

func TestApplyCatalogDateChangesIgnoresClosedMembership(t *testing.T) {
	db := newTestDB(t, 2)
	first := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	removed := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	returned := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)

	if _, err := RecordCatalogSnapshot(db, []string{"g1", "g2"}, first); err != nil {
		t.Fatalf("initial snapshot: %v", err)
	}
	targets, err := CurrentCatalogDateTargets(db)
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	var oldMembershipID int64
	for _, target := range targets {
		if target.GameID == "g1" {
			oldMembershipID = target.MembershipID
			break
		}
	}
	if oldMembershipID == 0 {
		t.Fatal("g1 membership not found")
	}

	if _, err := RecordCatalogSnapshot(db, []string{"g2"}, removed); err != nil {
		t.Fatalf("removal snapshot: %v", err)
	}
	if _, err := RecordCatalogSnapshot(db, []string{"g1", "g2"}, returned); err != nil {
		t.Fatalf("return snapshot: %v", err)
	}

	changed, err := ApplyCatalogDateChanges(db, []CatalogDateMatch{{
		MembershipID: oldMembershipID,
		AddedOn:      time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC),
		SourceURL:    "https://example.com/stale",
	}}, nil)
	if err != nil {
		t.Fatalf("apply stale match: %v", err)
	}
	if changed != 0 {
		t.Fatalf("changed=%d, want 0 for closed membership", changed)
	}

	var oldSource sql.NullString
	var oldRemoved sql.NullString
	if err := db.QueryRow(`
SELECT removed_on, added_on_source
FROM catalog_memberships WHERE id = ?`, oldMembershipID).Scan(&oldRemoved, &oldSource); err != nil {
		t.Fatalf("read old membership: %v", err)
	}
	if !oldRemoved.Valid || oldSource.Valid {
		t.Fatalf("old membership removed=%v source=%v; it must remain historical and untouched", oldRemoved, oldSource)
	}

	var newAddedOn, newSource string
	if err := db.QueryRow(`
SELECT date(added_on), added_on_source
FROM catalog_memberships
WHERE game_id = 'g1' AND removed_on IS NULL`).Scan(&newAddedOn, &newSource); err != nil {
		t.Fatalf("read new membership: %v", err)
	}
	if newAddedOn != "2026-07-20" || newSource != "observed" {
		t.Fatalf("new membership added=%q source=%q; stale match must not retarget it", newAddedOn, newSource)
	}
}

func TestApplyCatalogDateBackfillUpdatesClearsAndIsIdempotent(t *testing.T) {
	db := newTestDB(t, 2)
	observed := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	if _, err := RecordCatalogSnapshot(db, []string{"g1", "g2"}, observed); err != nil {
		t.Fatalf("initial snapshot: %v", err)
	}
	targets, err := CurrentCatalogDateTargets(db)
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	byID := make(map[string]CatalogDateTarget, len(targets))
	for _, target := range targets {
		byID[target.GameID] = target
	}
	if err := SetCatalogAddedDate(
		db,
		"g2",
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		"announcement",
		"https://example.com/wrong",
	); err != nil {
		t.Fatalf("seed date to clear: %v", err)
	}

	apply := func() int64 {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback()
		changed, err := ApplyCatalogDateBackfillTx(tx, []CatalogDateBackfillMatch{{
			MembershipID: byID["g1"].MembershipID,
			AddedOn:      time.Date(2022, 6, 23, 0, 0, 0, 0, time.UTC),
			SourceURL:    "https://example.com/history",
		}}, []int64{byID["g2"].MembershipID})
		if err != nil {
			t.Fatalf("apply backfill: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		return changed
	}

	if changed := apply(); changed != 2 {
		t.Fatalf("first changed=%d, want 2", changed)
	}
	var addedOn, source, sourceURL string
	if err := db.QueryRow(`
SELECT date(added_on), added_on_source, source_url
FROM catalog_memberships WHERE id = ?`, byID["g1"].MembershipID).Scan(&addedOn, &source, &sourceURL); err != nil {
		t.Fatalf("read verified date: %v", err)
	}
	if addedOn != "2022-06-23" || source != "verified" || sourceURL != "https://example.com/history" {
		t.Fatalf("g1 added=%q source=%q url=%q", addedOn, source, sourceURL)
	}
	var clearedDate, clearedSource, clearedURL sql.NullString
	if err := db.QueryRow(`
SELECT added_on, added_on_source, source_url
FROM catalog_memberships WHERE id = ?`, byID["g2"].MembershipID).Scan(&clearedDate, &clearedSource, &clearedURL); err != nil {
		t.Fatalf("read cleared date: %v", err)
	}
	if clearedDate.Valid || clearedSource.Valid || clearedURL.Valid {
		t.Fatalf("g2 must stay null: date=%v source=%v url=%v", clearedDate, clearedSource, clearedURL)
	}
	if changed := apply(); changed != 0 {
		t.Fatalf("idempotent changed=%d, want 0", changed)
	}
}

func TestApplyCatalogDateBackfillIgnoresClosedMembership(t *testing.T) {
	db := newTestDB(t, 2)
	first := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	removed := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	if _, err := RecordCatalogSnapshot(db, []string{"g1", "g2"}, first); err != nil {
		t.Fatalf("initial snapshot: %v", err)
	}
	targets, err := CurrentCatalogDateTargets(db)
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	var membershipID int64
	for _, target := range targets {
		if target.GameID == "g1" {
			membershipID = target.MembershipID
		}
	}
	if _, err := RecordCatalogSnapshot(db, []string{"g2"}, removed); err != nil {
		t.Fatalf("close membership: %v", err)
	}

	changes := []struct {
		name     string
		matches  []CatalogDateBackfillMatch
		keepNull []int64
	}{
		{
			name: "verified",
			matches: []CatalogDateBackfillMatch{{
				MembershipID: membershipID,
				AddedOn:      time.Date(2022, 6, 23, 0, 0, 0, 0, time.UTC),
				SourceURL:    "https://example.com/history",
			}},
		},
		{name: "keep-null", keepNull: []int64{membershipID}},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			changed, err := ApplyCatalogDateBackfillTx(tx, change.matches, change.keepNull)
			if err != nil {
				t.Fatalf("apply backfill: %v", err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("commit: %v", err)
			}
			if changed != 0 {
				t.Fatalf("changed=%d, want 0 for closed membership", changed)
			}
		})
	}
	var addedOn, source sql.NullString
	if err := db.QueryRow(`
SELECT added_on, added_on_source FROM catalog_memberships WHERE id = ?`, membershipID).Scan(&addedOn, &source); err != nil {
		t.Fatalf("read closed membership: %v", err)
	}
	if addedOn.Valid || source.Valid {
		t.Fatalf("closed membership changed: date=%v source=%v", addedOn, source)
	}
}

func TestValidateCatalogDateBackfillChangesRejectsDuplicateMemberships(t *testing.T) {
	valid := CatalogDateBackfillMatch{
		MembershipID: 1,
		AddedOn:      time.Date(2022, 6, 23, 0, 0, 0, 0, time.UTC),
		SourceURL:    "https://example.com/history",
	}
	tests := []struct {
		name     string
		matches  []CatalogDateBackfillMatch
		keepNull []int64
	}{
		{name: "duplicate verified", matches: []CatalogDateBackfillMatch{valid, valid}},
		{name: "verified and keep-null", matches: []CatalogDateBackfillMatch{valid}, keepNull: []int64{1}},
		{name: "duplicate keep-null", keepNull: []int64{1, 1}},
		{name: "zero date", matches: []CatalogDateBackfillMatch{{MembershipID: 1, SourceURL: valid.SourceURL}}},
		{name: "empty source", matches: []CatalogDateBackfillMatch{{MembershipID: 1, AddedOn: valid.AddedOn}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateCatalogDateBackfillChanges(tt.matches, tt.keepNull); err == nil {
				t.Fatal("invalid backfill changes must be rejected")
			}
		})
	}
}

func TestListGamesSortsByCatalogAddedDateWithUnknownLast(t *testing.T) {
	db := newTestDB(t, 3)
	observed := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if _, err := RecordCatalogSnapshot(db, []string{"g1", "g2", "g3"}, observed); err != nil {
		t.Fatalf("initial snapshot: %v", err)
	}
	if err := SetCatalogAddedDate(db, "g1", time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC), "announcement", "https://example.com/june"); err != nil {
		t.Fatalf("date g1: %v", err)
	}
	if err := SetCatalogAddedDate(db, "g2", time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC), "announcement", "https://example.com/july"); err != nil {
		t.Fatalf("date g2: %v", err)
	}

	res, err := ListGames(db, ListParams{Sort: "added", Order: "desc", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got, want := gameIDs(res.Games), []string{"g2", "g1", "g3"}; !sameStrings(got, want) {
		t.Fatalf("added desc got %v, ждали %v", got, want)
	}
	if got := res.Games[0].CatalogAddedLabel(); got != "21.07.2026" {
		t.Fatalf("added label=%q, ждали 21.07.2026", got)
	}
	if !res.Games[0].CatalogSourceURL.Valid || res.Games[0].CatalogSourceURL.String != "https://example.com/july" {
		t.Fatalf("source URL=%v", res.Games[0].CatalogSourceURL)
	}
}

func TestSetCatalogAddedDateRejectsEmptySource(t *testing.T) {
	db := newTestDB(t, 1)
	if _, err := RecordCatalogSnapshot(db, []string{"g1"}, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := SetCatalogAddedDate(db, "g1", time.Now(), "", ""); err == nil {
		t.Fatal("expected an error for an empty source")
	}
}

func TestSetCatalogAddedDateReturnsNotFoundWithoutOpenMembership(t *testing.T) {
	db := newTestDB(t, 1)
	err := SetCatalogAddedDate(
		db,
		"g1",
		time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		"announcement",
		"https://example.com/announcement",
	)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err=%v, want sql.ErrNoRows", err)
	}
}

func TestAddedIndexBucketsTreatsOrderCaseInsensitively(t *testing.T) {
	db := newTestDB(t, 2)
	observed := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if _, err := RecordCatalogSnapshot(db, []string{"g1", "g2"}, observed); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := SetCatalogAddedDate(db, "g1", time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC), "announcement", ""); err != nil {
		t.Fatalf("date g1: %v", err)
	}
	if err := SetCatalogAddedDate(db, "g2", time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC), "announcement", ""); err != nil {
		t.Fatalf("date g2: %v", err)
	}
	buckets, err := AddedIndexBuckets(db, ListParams{Sort: "added", Order: "DESC", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	if len(buckets) != 2 || buckets[0].Label != "2026 Июл" || buckets[0].Offset != 0 || buckets[1].Label != "2026 Июн" || buckets[1].Offset != 1 {
		t.Fatalf("buckets=%+v", buckets)
	}
}

func TestAddedIndexBucketsSupportsGenreFilter(t *testing.T) {
	db := newTestDB(t, 2)
	observed := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if _, err := RecordCatalogSnapshot(db, []string{"g1", "g2"}, observed); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := SetCatalogAddedDate(db, "g1", time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC), "announcement", ""); err != nil {
		t.Fatalf("date g1: %v", err)
	}
	if err := SetGenres(db, "g1", []string{"Action"}); err != nil {
		t.Fatalf("genres g1: %v", err)
	}
	if err := SetGenres(db, "g2", []string{"Adventure"}); err != nil {
		t.Fatalf("genres g2: %v", err)
	}

	buckets, err := AddedIndexBuckets(db, ListParams{
		Genres:   []string{"Action"},
		Sort:     "added",
		Order:    "desc",
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	if len(buckets) != 1 || buckets[0].Label != "2026 Июн" || buckets[0].Offset != 0 {
		t.Fatalf("buckets=%+v, want 2026 Июн @ 0", buckets)
	}
}

// TestAddedIndexBucketsAppendsNoDateBucketAfterDatedGroups проверяет, что игры
// без известной даты попадают в завершающий бакет «Без даты» с корректным
// смещением, накопленным из датированных групп (без отдельного запроса COUNT).
func TestAddedIndexBucketsAppendsNoDateBucketAfterDatedGroups(t *testing.T) {
	db := newTestDB(t, 2)
	observed := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if _, err := RecordCatalogSnapshot(db, []string{"g1", "g2"}, observed); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := SetCatalogAddedDate(db, "g1", time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC), "announcement", ""); err != nil {
		t.Fatalf("date g1: %v", err)
	}
	buckets, err := AddedIndexBuckets(db, ListParams{Sort: "added", Order: "asc", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("buckets=%+v, want 2", buckets)
	}
	if buckets[0].Label != "2026 Июн" || buckets[0].Offset != 0 {
		t.Fatalf("первый бакет=%+v, want 2026 Июн @ 0", buckets[0])
	}
	if buckets[1].Label != "Без даты" || buckets[1].Offset != 1 {
		t.Fatalf("бакет без даты=%+v, want Без даты @ 1", buckets[1])
	}
}

func TestOpenCriticURLUsesSavedDirectGamePage(t *testing.T) {
	g := GameView{
		TitleEn:           "Assassin's Creed Origins",
		OpenCriticPageURL: sql.NullString{String: "https://opencritic.com/game/4503/assassins-creed-origins", Valid: true},
	}
	want := "https://opencritic.com/game/4503/assassins-creed-origins"
	if got := g.OpenCriticURL(); got != want {
		t.Fatalf("OpenCriticURL=%q, ждали %q", got, want)
	}
}

func TestGamesNeedingOpenCriticSkipsFreshScoredRowsWithoutURL(t *testing.T) {
	db := newTestDB(t, 1)
	if err := UpdateOpenCritic(db, "g1", sql.NullInt64{Int64: 85, Valid: true}, sql.NullString{}); err != nil {
		t.Fatalf("update opencritic: %v", err)
	}
	targets, err := GamesNeedingOpenCritic(db, time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("ждали пустой список, получили %#v", targets)
	}
}

func TestGamesNeedingOpenCriticBackfillsStaleScoredRowsWithoutURL(t *testing.T) {
	db := newTestDB(t, 1)
	if err := UpdateOpenCritic(db, "g1", sql.NullInt64{Int64: 85, Valid: true}, sql.NullString{}); err != nil {
		t.Fatalf("update opencritic: %v", err)
	}
	if _, err := db.Exec(`UPDATE games SET oc_checked_at = ? WHERE id = ?`, time.Now().AddDate(0, 0, -45), "g1"); err != nil {
		t.Fatalf("age opencritic check: %v", err)
	}
	targets, err := GamesNeedingOpenCritic(db, time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	if len(targets) != 1 || targets[0].ID != "g1" {
		t.Fatalf("ждали stale backfill g1, получили %#v", targets)
	}
}

func TestGamesNeedingOpenCriticSkipsRowsWithFreshURL(t *testing.T) {
	db := newTestDB(t, 1)
	if err := UpdateOpenCritic(db, "g1", sql.NullInt64{Int64: 85, Valid: true}, sql.NullString{String: "https://opencritic.com/game/4503/assassins-creed-origins", Valid: true}); err != nil {
		t.Fatalf("update opencritic: %v", err)
	}
	targets, err := GamesNeedingOpenCritic(db, time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("ждали пустой список, получили %#v", targets)
	}
}

func TestGamesNeedingOpenCriticRefreshesStaleRowsWithURL(t *testing.T) {
	db := newTestDB(t, 1)
	if err := UpdateOpenCritic(db, "g1", sql.NullInt64{Int64: 85, Valid: true}, sql.NullString{String: "https://opencritic.com/game/4503/assassins-creed-origins", Valid: true}); err != nil {
		t.Fatalf("update opencritic: %v", err)
	}
	if _, err := db.Exec(`UPDATE games SET oc_checked_at = ? WHERE id = ?`, time.Now().AddDate(0, 0, -45), "g1"); err != nil {
		t.Fatalf("age opencritic check: %v", err)
	}
	targets, err := GamesNeedingOpenCritic(db, time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	if len(targets) != 1 || targets[0].ID != "g1" {
		t.Fatalf("ждали stale refresh g1, получили %#v", targets)
	}
}

func TestSetSourceGenresReplacesPerSourceOnly(t *testing.T) {
	db := newTestDB(t, 1)
	if err := SetSourceGenres(db, "g1", "metacritic", []SourceGenre{{Genre: "3D Platformer"}}); err != nil {
		t.Fatalf("set metacritic genres: %v", err)
	}
	if err := SetSourceGenres(db, "g1", "opencritic", []SourceGenre{{Genre: "Adventure", SourceGenreID: sql.NullInt64{Int64: 76, Valid: true}}, {Genre: "Platformer"}}); err != nil {
		t.Fatalf("set opencritic genres: %v", err)
	}
	if err := SetSourceGenres(db, "g1", "opencritic", []SourceGenre{{Genre: "Platformer", SourceGenreID: sql.NullInt64{Int64: 82, Valid: true}}, {Genre: "Platformer"}}); err != nil {
		t.Fatalf("replace opencritic genres: %v", err)
	}
	got, err := SourceGenres(db, "g1")
	if err != nil {
		t.Fatalf("source genres: %v", err)
	}
	want := map[string][]string{
		"metacritic": []string{"3D Platformer"},
		"opencritic": []string{"Platformer"},
	}
	if len(got) != len(want) {
		t.Fatalf("sources=%v, ждали %v", got, want)
	}
	for source, genres := range want {
		if len(got[source]) != len(genres) {
			t.Fatalf("%s genres=%v, ждали %v", source, got[source], genres)
		}
		for i := range genres {
			if got[source][i] != genres[i] {
				t.Fatalf("%s genres=%v, ждали %v", source, got[source], genres)
			}
		}
	}
}

func TestUpdateStoresUserScoresAndRecomputesAllAverages(t *testing.T) {
	db := newTestDB(t, 1)
	if err := UpdateMetacriticScores(
		db,
		"g1",
		sql.NullInt64{Int64: 80, Valid: true},
		sql.NullInt64{Int64: 65, Valid: true},
		sql.NullInt64{Int64: 120, Valid: true},
		sql.NullString{String: "https://www.metacritic.com/game/example/", Valid: true},
	); err != nil {
		t.Fatalf("update metacritic: %v", err)
	}
	if err := UpdateOpenCriticScores(
		db,
		"g1",
		sql.NullInt64{Int64: 90, Valid: true},
		sql.NullString{String: "https://opencritic.com/game/1660/assassins-creed-syndicate", Valid: true},
		sql.NullInt64{Int64: 1660, Valid: true},
		sql.NullInt64{Int64: 70, Valid: true},
		sql.NullInt64{Int64: 57, Valid: true},
	); err != nil {
		t.Fatalf("update opencritic: %v", err)
	}
	if err := UpdateHLTB(
		db,
		"g1",
		sql.NullInt64{Int64: 3600, Valid: true},
		sql.NullInt64{Int64: 75, Valid: true},
		sql.NullInt64{Int64: 123, Valid: true},
		sql.NullString{String: "https://howlongtobeat.com/game/123", Valid: true},
	); err != nil {
		t.Fatalf("update hltb: %v", err)
	}

	var mcUser, mcUserCount, ocID, ocPlayer, ocPlayerCount sql.NullInt64
	var mcURL sql.NullString
	var avg, criticAvg, playerAvg sql.NullFloat64
	if err := db.QueryRow(`
SELECT metacritic_user_score, metacritic_user_count, metacritic_url,
       opencritic_id, opencritic_player_score, opencritic_player_count,
       average_score, critic_average_score, player_average_score
FROM games WHERE id = ?`, "g1").Scan(
		&mcUser, &mcUserCount, &mcURL, &ocID, &ocPlayer, &ocPlayerCount,
		&avg, &criticAvg, &playerAvg,
	); err != nil {
		t.Fatalf("select: %v", err)
	}
	if !mcUser.Valid || mcUser.Int64 != 65 {
		t.Fatalf("metacritic_user_score=%v, ждали 65", mcUser)
	}
	if !mcUserCount.Valid || mcUserCount.Int64 != 120 {
		t.Fatalf("metacritic_user_count=%v, ждали 120", mcUserCount)
	}
	if !mcURL.Valid || mcURL.String != "https://www.metacritic.com/game/example/" {
		t.Fatalf("metacritic_url=%v", mcURL)
	}
	if !ocID.Valid || ocID.Int64 != 1660 {
		t.Fatalf("opencritic_id=%v, ждали 1660", ocID)
	}
	if !ocPlayer.Valid || ocPlayer.Int64 != 70 {
		t.Fatalf("opencritic_player_score=%v, ждали 70", ocPlayer)
	}
	if !ocPlayerCount.Valid || ocPlayerCount.Int64 != 57 {
		t.Fatalf("opencritic_player_count=%v, ждали 57", ocPlayerCount)
	}
	if !avg.Valid || avg.Float64 != 76.7 {
		t.Fatalf("average_score=%v, ждали 76.7 с пониженным весом OC player", avg)
	}
	if !criticAvg.Valid || criticAvg.Float64 != 85 {
		t.Fatalf("critic_average_score=%v, ждали 85", criticAvg)
	}
	if !playerAvg.Valid || playerAvg.Float64 != 70 {
		t.Fatalf("player_average_score=%v, ждали 70", playerAvg)
	}
}

func TestRecomputeAveragesSkipsZeroScores(t *testing.T) {
	db := newTestDB(t, 1)
	if err := UpdateMetacriticScores(
		db,
		"g1",
		sql.NullInt64{Int64: 0, Valid: true},
		sql.NullInt64{Int64: 80, Valid: true},
		sql.NullInt64{Int64: 10, Valid: true},
		sql.NullString{},
	); err != nil {
		t.Fatalf("update metacritic: %v", err)
	}
	if err := UpdateOpenCriticScores(
		db,
		"g1",
		sql.NullInt64{Int64: 0, Valid: true},
		sql.NullString{},
		sql.NullInt64{},
		sql.NullInt64{Int64: 0, Valid: true},
		sql.NullInt64{Int64: 0, Valid: true},
	); err != nil {
		t.Fatalf("update opencritic: %v", err)
	}
	if err := UpdateHLTB(
		db,
		"g1",
		sql.NullInt64{},
		sql.NullInt64{Int64: 70, Valid: true},
		sql.NullInt64{},
		sql.NullString{},
	); err != nil {
		t.Fatalf("update hltb: %v", err)
	}

	var avg, criticAvg, playerAvg sql.NullFloat64
	if err := db.QueryRow(`
SELECT average_score, critic_average_score, player_average_score
FROM games WHERE id = ?`, "g1").Scan(&avg, &criticAvg, &playerAvg); err != nil {
		t.Fatalf("select: %v", err)
	}
	if !avg.Valid || avg.Float64 != 75 {
		t.Fatalf("average_score=%v, ждали 75", avg)
	}
	if criticAvg.Valid {
		t.Fatalf("critic_average_score=%v, ждали NULL", criticAvg)
	}
	if !playerAvg.Valid || playerAvg.Float64 != 75 {
		t.Fatalf("player_average_score=%v, ждали 75", playerAvg)
	}
}

func TestRecomputeAveragesWeightsOpenCriticPlayerByVoteCount(t *testing.T) {
	tests := []struct {
		name       string
		ocPlayers  int64
		wantAvg    float64
		wantPlayer float64
	}{
		{
			name:       "less than 20 OpenCritic player votes are ignored",
			ocPlayers:  4,
			wantAvg:    72.0,
			wantPlayer: 65.0,
		},
		{
			name:       "20 to 100 OpenCritic player votes use reduced weight",
			ocPlayers:  20,
			wantAvg:    65.1,
			wantPlayer: 54.0,
		},
		{
			name:       "more than 100 OpenCritic player votes are equal weight",
			ocPlayers:  101,
			wantAvg:    59.6,
			wantPlayer: 46.7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t, 1)
			if err := UpdateMetacriticScores(
				db,
				"g1",
				sql.NullInt64{Int64: 80, Valid: true},
				sql.NullInt64{Int64: 62, Valid: true},
				sql.NullInt64{Int64: 32, Valid: true},
				sql.NullString{},
			); err != nil {
				t.Fatalf("update metacritic: %v", err)
			}
			if err := UpdateOpenCriticScores(
				db,
				"g1",
				sql.NullInt64{Int64: 78, Valid: true},
				sql.NullString{},
				sql.NullInt64{},
				sql.NullInt64{Int64: 10, Valid: true},
				sql.NullInt64{Int64: tt.ocPlayers, Valid: true},
			); err != nil {
				t.Fatalf("update opencritic: %v", err)
			}
			if err := UpdateHLTB(
				db,
				"g1",
				sql.NullInt64{},
				sql.NullInt64{Int64: 68, Valid: true},
				sql.NullInt64{},
				sql.NullString{},
			); err != nil {
				t.Fatalf("update hltb: %v", err)
			}

			var avg, playerAvg sql.NullFloat64
			if err := db.QueryRow(`
SELECT average_score, player_average_score
FROM games WHERE id = ?`, "g1").Scan(&avg, &playerAvg); err != nil {
				t.Fatalf("select: %v", err)
			}
			assertFloat(t, "average_score", avg, tt.wantAvg)
			assertFloat(t, "player_average_score", playerAvg, tt.wantPlayer)
		})
	}
}

func assertFloat(t *testing.T, name string, got sql.NullFloat64, want float64) {
	t.Helper()
	if !got.Valid {
		t.Fatalf("%s=NULL, ждали %.1f", name, want)
	}
	if math.Abs(got.Float64-want) > 0.05 {
		t.Fatalf("%s=%.1f, ждали %.1f", name, got.Float64, want)
	}
}

func TestListGamesFiltersByCriticAndPlayerAverages(t *testing.T) {
	db := newTestDB(t, 3)
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("exec %q: %v", query, err)
		}
	}
	mustExec(`UPDATE games SET critic_average_score = 90, player_average_score = 70 WHERE id = 'g1'`)
	mustExec(`UPDATE games SET critic_average_score = 60, player_average_score = 95 WHERE id = 'g2'`)
	mustExec(`UPDATE games SET critic_average_score = 82, player_average_score = 85 WHERE id = 'g3'`)

	res, err := ListGames(db, ListParams{CriticFrom: 80, PlayerFrom: 80, Page: 1, PageSize: 24})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Games) != 1 || res.Games[0].ID != "g3" {
		t.Fatalf("ждали только g3, получили %#v", res.Games)
	}
}

func TestListGamesSortsByCriticAndPlayerAverages(t *testing.T) {
	db := newTestDB(t, 4)
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("exec %q: %v", query, err)
		}
	}
	mustExec(`UPDATE games SET critic_average_score = 90, player_average_score = 70 WHERE id = 'g1'`)
	mustExec(`UPDATE games SET critic_average_score = 60, player_average_score = 95 WHERE id = 'g2'`)
	mustExec(`UPDATE games SET critic_average_score = 82, player_average_score = 85 WHERE id = 'g3'`)
	mustExec(`UPDATE games SET critic_average_score = NULL, player_average_score = NULL WHERE id = 'g4'`)

	criticRes, err := ListGames(db, ListParams{Sort: "critic", Order: "desc", Page: 1, PageSize: 24})
	if err != nil {
		t.Fatalf("list critic: %v", err)
	}
	gotCritic := []string{criticRes.Games[0].ID, criticRes.Games[1].ID, criticRes.Games[2].ID, criticRes.Games[3].ID}
	wantCritic := []string{"g1", "g3", "g2", "g4"}
	if !equalStrings(gotCritic, wantCritic) {
		t.Fatalf("critic order=%v, ждали %v", gotCritic, wantCritic)
	}

	playerRes, err := ListGames(db, ListParams{Sort: "player", Order: "desc", Page: 1, PageSize: 24})
	if err != nil {
		t.Fatalf("list player: %v", err)
	}
	gotPlayer := []string{playerRes.Games[0].ID, playerRes.Games[1].ID, playerRes.Games[2].ID, playerRes.Games[3].ID}
	wantPlayer := []string{"g2", "g3", "g1", "g4"}
	if !equalStrings(gotPlayer, wantPlayer) {
		t.Fatalf("player order=%v, ждали %v", gotPlayer, wantPlayer)
	}
}

func TestListGamesLoadsUserScoreFieldsAndAverages(t *testing.T) {
	db := newTestDB(t, 1)
	if _, err := db.Exec(`
UPDATE games
SET metacritic_score = 80,
    metacritic_user_score = 65,
    metacritic_user_count = 120,
    opencritic_score = 90,
    opencritic_player_score = 70,
    opencritic_player_count = 57,
    average_score = 76,
    critic_average_score = 85,
    player_average_score = 67.5
WHERE id = 'g1'`); err != nil {
		t.Fatalf("update: %v", err)
	}

	res, err := ListGames(db, ListParams{Page: 1, PageSize: 24})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Games) != 1 {
		t.Fatalf("games=%d, ждали 1", len(res.Games))
	}
	g := res.Games[0]
	if !g.MetacriticUser.Valid || g.MetacriticUser.Int64 != 65 {
		t.Fatalf("MetacriticUser=%v, ждали 65", g.MetacriticUser)
	}
	if !g.MetacriticUserCount.Valid || g.MetacriticUserCount.Int64 != 120 {
		t.Fatalf("MetacriticUserCount=%v, ждали 120", g.MetacriticUserCount)
	}
	if !g.OpenCriticPlayer.Valid || g.OpenCriticPlayer.Int64 != 70 {
		t.Fatalf("OpenCriticPlayer=%v, ждали 70", g.OpenCriticPlayer)
	}
	if !g.OpenCriticPlayerCount.Valid || g.OpenCriticPlayerCount.Int64 != 57 {
		t.Fatalf("OpenCriticPlayerCount=%v, ждали 57", g.OpenCriticPlayerCount)
	}
	if !g.CriticAverage.Valid || g.CriticAverage.Float64 != 85 {
		t.Fatalf("CriticAverage=%v, ждали 85", g.CriticAverage)
	}
	if !g.PlayerAverage.Valid || g.PlayerAverage.Float64 != 67.5 {
		t.Fatalf("PlayerAverage=%v, ждали 67.5", g.PlayerAverage)
	}
}

func TestHLTBURLUsesDirectGamePageWhenKnown(t *testing.T) {
	g := GameView{
		TitleEn:     "Assassin's Creed Origins",
		HLTBPageURL: sql.NullString{String: "https://howlongtobeat.com/game/46402", Valid: true},
	}
	want := "https://howlongtobeat.com/game/46402"
	if got := g.HLTBURL(); got != want {
		t.Fatalf("HLTBURL=%q, ждали %q", got, want)
	}
}

func TestMetacriticURLUsesStoredPageWhenKnown(t *testing.T) {
	g := GameView{
		TitleEn:           "No More Heroes 3",
		MetacriticPageURL: sql.NullString{String: "https://www.metacritic.com/game/no-more-heroes-iii/", Valid: true},
	}
	want := "https://www.metacritic.com/game/no-more-heroes-iii/"
	if got := g.MetacriticURL(); got != want {
		t.Fatalf("MetacriticURL=%q, ждали %q", got, want)
	}
}

func TestMetacriticURLSearchesWhenPageIsUnknown(t *testing.T) {
	for _, titleEn := range []string{
		"The Long Dark PS4 & PS5",
		"Hollow Knight Voidheart Edition",
	} {
		g := GameView{TitleEn: titleEn}
		want := "https://www.metacritic.com/search/" + url.PathEscape(titleEn) + "/"
		if got := g.MetacriticURL(); got != want {
			t.Fatalf("MetacriticURL(%q)=%q, ждали %q", titleEn, got, want)
		}
	}
}

func TestGamesNeedingMetacriticBackfillsFreshScoredRowsWithoutURL(t *testing.T) {
	db := newTestDB(t, 4)
	if err := UpdateMetacriticScores(
		db, "g1",
		sql.NullInt64{Int64: 77, Valid: true},
		sql.NullInt64{}, sql.NullInt64{}, sql.NullString{},
	); err != nil {
		t.Fatalf("update g1: %v", err)
	}
	if err := UpdateMetacriticScores(
		db, "g2",
		sql.NullInt64{Int64: 78, Valid: true},
		sql.NullInt64{}, sql.NullInt64{},
		sql.NullString{String: "https://www.metacritic.com/game/example/", Valid: true},
	); err != nil {
		t.Fatalf("update g2: %v", err)
	}
	if err := UpdateMetacriticScores(
		db, "g3",
		sql.NullInt64{Int64: 79, Valid: true},
		sql.NullInt64{}, sql.NullInt64{}, sql.NullString{},
	); err != nil {
		t.Fatalf("update g3: %v", err)
	}
	if _, err := db.Exec(`UPDATE games SET mc_checked_at = ? WHERE id = ?`, time.Now().AddDate(0, 0, -45), "g3"); err != nil {
		t.Fatalf("age g3 check: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE games
		SET metacritic_score = ?, metacritic_url = NULL, mc_checked_at = NULL
		WHERE id = ?`, 80, "g4"); err != nil {
		t.Fatalf("seed g4 without check: %v", err)
	}

	targets, err := GamesNeedingMetacritic(db, time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	if len(targets) != 3 || targets[0].ID != "g1" || targets[1].ID != "g3" || targets[2].ID != "g4" {
		t.Fatalf("ждали g1, g3 и g4, получили %#v", targets)
	}
	if !targets[0].NeedsMetacriticURLBackfill {
		t.Fatalf("g1 должна быть URL backfill целью: %#v", targets[0])
	}
	if targets[1].NeedsMetacriticURLBackfill {
		t.Fatalf("устаревшая g3 не должна быть URL backfill целью: %#v", targets[1])
	}
	if targets[2].NeedsMetacriticURLBackfill {
		t.Fatalf("g4 без времени проверки не должна быть URL backfill целью: %#v", targets[2])
	}
}

func TestGamesNeedingHLTBSkipsFreshScoredRowsWithoutURL(t *testing.T) {
	db := newTestDB(t, 1)
	if err := UpdateHLTB(db, "g1", sql.NullInt64{Int64: 189183, Valid: true}, sql.NullInt64{Int64: 79, Valid: true}, sql.NullInt64{}, sql.NullString{}); err != nil {
		t.Fatalf("update hltb: %v", err)
	}
	targets, err := GamesNeedingHLTB(db, time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("ждали пустой список, получили %#v", targets)
	}
}

func TestGamesNeedingHLTBBackfillsStaleScoredRowsWithoutURL(t *testing.T) {
	db := newTestDB(t, 1)
	if err := UpdateHLTB(db, "g1", sql.NullInt64{Int64: 189183, Valid: true}, sql.NullInt64{Int64: 79, Valid: true}, sql.NullInt64{}, sql.NullString{}); err != nil {
		t.Fatalf("update hltb: %v", err)
	}
	if _, err := db.Exec(`UPDATE games SET hltb_checked_at = ? WHERE id = ?`, time.Now().AddDate(0, 0, -45), "g1"); err != nil {
		t.Fatalf("age hltb check: %v", err)
	}
	targets, err := GamesNeedingHLTB(db, time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	if len(targets) != 1 || targets[0].ID != "g1" {
		t.Fatalf("ждали stale hltb backfill g1, получили %#v", targets)
	}
}

func TestGamesNeedingHLTBRefreshesStaleRowsWithURL(t *testing.T) {
	db := newTestDB(t, 1)
	if err := UpdateHLTB(db, "g1", sql.NullInt64{Int64: 189183, Valid: true}, sql.NullInt64{Int64: 79, Valid: true}, sql.NullInt64{Int64: 46402, Valid: true}, sql.NullString{String: "https://howlongtobeat.com/game/46402", Valid: true}); err != nil {
		t.Fatalf("update hltb: %v", err)
	}
	if _, err := db.Exec(`UPDATE games SET hltb_checked_at = ? WHERE id = ?`, time.Now().AddDate(0, 0, -45), "g1"); err != nil {
		t.Fatalf("age hltb check: %v", err)
	}
	targets, err := GamesNeedingHLTB(db, time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	if len(targets) != 1 || targets[0].ID != "g1" {
		t.Fatalf("ждали stale hltb refresh g1, получили %#v", targets)
	}
}

func TestGameViewCatalogAddedShortLabel(t *testing.T) {
	g := GameView{}
	if g.CatalogAddedShortLabel() != "" {
		t.Fatalf("пустая дата должна давать пустую строку")
	}
	withDate := GameView{CatalogAddedOn: sql.NullTime{Time: time.Date(2022, 6, 23, 0, 0, 0, 0, time.UTC), Valid: true}}
	if got := withDate.CatalogAddedShortLabel(); got != "23.06.22" {
		t.Fatalf("short label=%q, want 23.06.22", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
