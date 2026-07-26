package sqlite

import (
	"path/filepath"
	"testing"
)

func TestListGamesTitleSortIsCaseInsensitive(t *testing.T) {
	db, err := openTestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, g := range []GameRow{
		{ID: "g1", Title: "ALIENATION"},
		{ID: "g2", Title: "a Space for the Unbound"},
		{ID: "g3", Title: "Beta Game"},
	} {
		if err := UpsertGame(db, g); err != nil {
			t.Fatalf("upsert %s: %v", g.ID, err)
		}
	}

	res, err := ListGames(db, ListParams{Sort: "title", Order: "asc", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var got []string
	for _, g := range res.Games {
		got = append(got, g.Title)
	}
	want := []string{"a Space for the Unbound", "ALIENATION", "Beta Game"}
	if len(got) != len(want) {
		t.Fatalf("got %v, ждали %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("порядок %v, ждали %v (NOCASE)", got, want)
		}
	}
}

func TestTitleIndexBucketsCumulativeOffsets(t *testing.T) {
	db, err := openTestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, g := range []GameRow{
		{ID: "g1", Title: "Alpha"},
		{ID: "g2", Title: "angel"}, // нижний регистр — тот же бакет A
		{ID: "g3", Title: "Beta"},
		{ID: "g4", Title: "42 Game"}, // не буква — бакет "#"
	} {
		if err := UpsertGame(db, g); err != nil {
			t.Fatalf("upsert %s: %v", g.ID, err)
		}
	}

	got, err := TitleIndexBuckets(db, ListParams{Sort: "title", Order: "asc"})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	want := []IndexBucket{{Label: "#", Offset: 0}, {Label: "A", Offset: 1}, {Label: "B", Offset: 3}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, ждали %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, ждали %+v", got, want)
		}
	}
}

func TestTitleIndexBucketsDescReversesOrder(t *testing.T) {
	db, err := openTestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, g := range []GameRow{
		{ID: "g1", Title: "Alpha"},
		{ID: "g2", Title: "Beta"},
	} {
		if err := UpsertGame(db, g); err != nil {
			t.Fatalf("upsert %s: %v", g.ID, err)
		}
	}

	got, err := TitleIndexBuckets(db, ListParams{Sort: "title", Order: "desc"})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	want := []IndexBucket{{Label: "B", Offset: 0}, {Label: "A", Offset: 1}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, ждали %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, ждали %+v", got, want)
		}
	}
}

func TestTitleIndexBucketsRespectsFilters(t *testing.T) {
	db, err := openTestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, g := range []GameRow{
		{ID: "g1", Title: "Alpha", ReleaseYear: 2020},
		{ID: "g2", Title: "Beta", ReleaseYear: 2010},
	} {
		if err := UpsertGame(db, g); err != nil {
			t.Fatalf("upsert %s: %v", g.ID, err)
		}
	}

	got, err := TitleIndexBuckets(db, ListParams{Sort: "title", Order: "asc", YearFrom: 2015})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	want := []IndexBucket{{Label: "A", Offset: 0}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %+v, ждали %+v (фильтр по году должен применяться)", got, want)
	}
}

func TestIndexBucketsYearAscDescAndZeroYear(t *testing.T) {
	db, err := openTestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, g := range []GameRow{
		{ID: "g1", Title: "A", ReleaseYear: 2020},
		{ID: "g2", Title: "B", ReleaseYear: 2010},
		{ID: "g3", Title: "C", ReleaseYear: 2020},
		{ID: "g4", Title: "D"}, // год 0 — бакет «—»
	} {
		if err := UpsertGame(db, g); err != nil {
			t.Fatalf("upsert %s: %v", g.ID, err)
		}
	}

	got, err := IndexBuckets(db, ListParams{Sort: "year", Order: "asc"})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	want := []IndexBucket{{Label: "—", Offset: 0}, {Label: "2010", Offset: 1}, {Label: "2020", Offset: 2}}
	assertBuckets(t, got, want)

	got, err = IndexBuckets(db, ListParams{Sort: "year", Order: "desc"})
	if err != nil {
		t.Fatalf("buckets desc: %v", err)
	}
	want = []IndexBucket{{Label: "2020", Offset: 0}, {Label: "2010", Offset: 2}, {Label: "—", Offset: 3}}
	assertBuckets(t, got, want)
}

func TestIndexBucketsDispatch(t *testing.T) {
	db, err := openTestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := UpsertGame(db, GameRow{ID: "g1", Title: "Alpha", ReleaseYear: 2020}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// title — буквы
	got, err := IndexBuckets(db, ListParams{Sort: "title", Order: "asc"})
	if err != nil || len(got) != 1 || got[0].Label != "A" {
		t.Fatalf("title: got %+v err %v, ждали бакет A", got, err)
	}
	// неизвестная сортировка — без индекса
	got, err = IndexBuckets(db, ListParams{Sort: "average", Order: "asc"})
	if err != nil || got != nil {
		t.Fatalf("average: got %+v err %v, ждали nil", got, err)
	}
}

func assertBuckets(t *testing.T, got, want []IndexBucket) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %+v, ждали %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, ждали %+v", got, want)
		}
	}
}

func TestIndexBucketsScoreDecades(t *testing.T) {
	db, err := openTestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, g := range []GameRow{
		{ID: "g1", Title: "A"},
		{ID: "g2", Title: "B"},
		{ID: "g3", Title: "C"}, // без оценки — NULL-хвост, чипа нет
	} {
		if err := UpsertGame(db, g); err != nil {
			t.Fatalf("upsert %s: %v", g.ID, err)
		}
	}
	// дробная средняя 79.5 должна попасть в декаду 70
	if _, err := db.Exec(`UPDATE games SET critic_average_score = 79.5 WHERE id = 'g1'`); err != nil {
		t.Fatalf("update g1: %v", err)
	}
	if _, err := db.Exec(`UPDATE games SET critic_average_score = 85 WHERE id = 'g2'`); err != nil {
		t.Fatalf("update g2: %v", err)
	}

	got, err := IndexBuckets(db, ListParams{Sort: "critic", Order: "asc"})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	assertBuckets(t, got, []IndexBucket{{Label: "70", Offset: 0}, {Label: "80", Offset: 1}})

	got, err = IndexBuckets(db, ListParams{Sort: "critic", Order: "desc"})
	if err != nil {
		t.Fatalf("buckets desc: %v", err)
	}
	assertBuckets(t, got, []IndexBucket{{Label: "80", Offset: 0}, {Label: "70", Offset: 1}})
}

func TestIndexBucketsPlayerDecades(t *testing.T) {
	db, err := openTestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := UpsertGame(db, GameRow{ID: "g1", Title: "A"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := db.Exec(`UPDATE games SET player_average_score = 91 WHERE id = 'g1'`); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := IndexBuckets(db, ListParams{Sort: "player", Order: "desc"})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	assertBuckets(t, got, []IndexBucket{{Label: "90", Offset: 0}})
}

func TestIndexBucketsHLTBThresholds(t *testing.T) {
	db, err := openTestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, g := range []GameRow{
		{ID: "g1", Title: "A"},
		{ID: "g2", Title: "B"},
		{ID: "g3", Title: "C"},
		{ID: "g4", Title: "D"}, // без времени — NULL-хвост
	} {
		if err := UpsertGame(db, g); err != nil {
			t.Fatalf("upsert %s: %v", g.ID, err)
		}
	}
	// 3 ч, 15 ч и 65 ч (в секундах)
	for id, hours := range map[string]int{"g1": 3, "g2": 15, "g3": 65} {
		if _, err := db.Exec(`UPDATE games SET hltb_main_extra = ? WHERE id = ?`, hours*3600, id); err != nil {
			t.Fatalf("update %s: %v", id, err)
		}
	}

	got, err := IndexBuckets(db, ListParams{Sort: "hltbmain", Order: "asc"})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	assertBuckets(t, got, []IndexBucket{{Label: "0–5", Offset: 0}, {Label: "10–20", Offset: 1}, {Label: "60+", Offset: 2}})

	got, err = IndexBuckets(db, ListParams{Sort: "hltbmain", Order: "desc"})
	if err != nil {
		t.Fatalf("buckets desc: %v", err)
	}
	assertBuckets(t, got, []IndexBucket{{Label: "60+", Offset: 0}, {Label: "10–20", Offset: 1}, {Label: "0–5", Offset: 2}})
}

func TestListGamesFiltersByReviewCountSum(t *testing.T) {
	db, err := openTestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, g := range []GameRow{
		{ID: "g1", Title: "Few Reviews"},
		{ID: "g2", Title: "Enough Reviews"},
		{ID: "g3", Title: "Many Reviews"},
		{ID: "g4", Title: "No Reviews"},
	} {
		if err := UpsertGame(db, g); err != nil {
			t.Fatalf("upsert %s: %v", g.ID, err)
		}
	}
	updates := map[string][2]int{
		"g1": {10, 20},    // 30
		"g2": {800, 250},  // 1050
		"g3": {3000, 400}, // 3400
	}
	for id, counts := range updates {
		if _, err := db.Exec(`UPDATE games SET metacritic_user_count = ?, opencritic_player_count = ? WHERE id = ?`, counts[0], counts[1], id); err != nil {
			t.Fatalf("update %s: %v", id, err)
		}
	}

	res, err := ListGames(db, ListParams{ReviewsFrom: 1000, ReviewsTo: 2000, Sort: "title", Order: "asc", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res.Total != 1 || len(res.Games) != 1 || res.Games[0].ID != "g2" {
		t.Fatalf("got total=%d games=%v, ждали только g2", res.Total, gameIDs(res.Games))
	}
}

func TestListGamesSortsByReviewCountSum(t *testing.T) {
	db, err := openTestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, g := range []GameRow{
		{ID: "g1", Title: "A"},
		{ID: "g2", Title: "B"},
		{ID: "g3", Title: "C"},
	} {
		if err := UpsertGame(db, g); err != nil {
			t.Fatalf("upsert %s: %v", g.ID, err)
		}
	}
	updates := map[string][2]int{
		"g1": {100, 0},  // 100
		"g2": {20, 400}, // 420
		"g3": {0, 0},    // 0
	}
	for id, counts := range updates {
		if _, err := db.Exec(`UPDATE games SET metacritic_user_count = ?, opencritic_player_count = ? WHERE id = ?`, counts[0], counts[1], id); err != nil {
			t.Fatalf("update %s: %v", id, err)
		}
	}

	res, err := ListGames(db, ListParams{Sort: "reviews", Order: "desc", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list desc: %v", err)
	}
	if got, want := gameIDs(res.Games), []string{"g2", "g1", "g3"}; !sameStrings(got, want) {
		t.Fatalf("desc got %v, ждали %v", got, want)
	}

	res, err = ListGames(db, ListParams{Sort: "reviews", Order: "asc", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list asc: %v", err)
	}
	if got, want := gameIDs(res.Games), []string{"g3", "g1", "g2"}; !sameStrings(got, want) {
		t.Fatalf("asc got %v, ждали %v", got, want)
	}
}

func TestIndexBucketsReviewCountThresholds(t *testing.T) {
	db, err := openTestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, g := range []GameRow{
		{ID: "g1", Title: "A"},
		{ID: "g2", Title: "B"},
		{ID: "g3", Title: "C"},
	} {
		if err := UpsertGame(db, g); err != nil {
			t.Fatalf("upsert %s: %v", g.ID, err)
		}
	}
	updates := map[string][2]int{
		"g1": {0, 0},       // 0
		"g2": {700, 800},   // 1k-4.9k
		"g3": {3000, 2500}, // 5k+
	}
	for id, counts := range updates {
		if _, err := db.Exec(`UPDATE games SET metacritic_user_count = ?, opencritic_player_count = ? WHERE id = ?`, counts[0], counts[1], id); err != nil {
			t.Fatalf("update %s: %v", id, err)
		}
	}

	got, err := IndexBuckets(db, ListParams{Sort: "reviews", Order: "desc"})
	if err != nil {
		t.Fatalf("buckets desc: %v", err)
	}
	assertBuckets(t, got, []IndexBucket{{Label: "5к+", Offset: 0}, {Label: "1к–4.9к", Offset: 1}, {Label: "0", Offset: 2}})
}

func gameIDs(games []GameView) []string {
	ids := make([]string, 0, len(games))
	for _, g := range games {
		ids = append(ids, g.ID)
	}
	return ids
}

func sameStrings(a, b []string) bool {
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
