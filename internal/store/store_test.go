package store

import (
	"database/sql"
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

func TestUpdateStoresUserScoresAndRecomputesAllAverages(t *testing.T) {
	db := newTestDB(t, 1)
	if err := UpdateMetacriticScores(
		db,
		"g1",
		sql.NullInt64{Int64: 80, Valid: true},
		sql.NullInt64{Int64: 65, Valid: true},
		sql.NullInt64{Int64: 120, Valid: true},
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
	var avg, criticAvg, playerAvg sql.NullFloat64
	if err := db.QueryRow(`
SELECT metacritic_user_score, metacritic_user_count,
       opencritic_id, opencritic_player_score, opencritic_player_count,
       average_score, critic_average_score, player_average_score
FROM games WHERE id = ?`, "g1").Scan(
		&mcUser, &mcUserCount, &ocID, &ocPlayer, &ocPlayerCount,
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
	if !ocID.Valid || ocID.Int64 != 1660 {
		t.Fatalf("opencritic_id=%v, ждали 1660", ocID)
	}
	if !ocPlayer.Valid || ocPlayer.Int64 != 70 {
		t.Fatalf("opencritic_player_score=%v, ждали 70", ocPlayer)
	}
	if !ocPlayerCount.Valid || ocPlayerCount.Int64 != 57 {
		t.Fatalf("opencritic_player_count=%v, ждали 57", ocPlayerCount)
	}
	if !avg.Valid || avg.Float64 != 76 {
		t.Fatalf("average_score=%v, ждали 76 по пяти источникам", avg)
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
