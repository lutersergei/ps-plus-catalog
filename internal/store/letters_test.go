package store

import (
	"path/filepath"
	"testing"
)

func TestListGamesTitleSortIsCaseInsensitive(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
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
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
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
	want := []IndexBucket{{"#", 0}, {"A", 1}, {"B", 3}}
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
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
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
	want := []IndexBucket{{"B", 0}, {"A", 1}}
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
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
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
	want := []IndexBucket{{"A", 0}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %+v, ждали %+v (фильтр по году должен применяться)", got, want)
	}
}

func TestIndexBucketsYearAscDescAndZeroYear(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
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
	want := []IndexBucket{{"—", 0}, {"2010", 1}, {"2020", 2}}
	assertBuckets(t, got, want)

	got, err = IndexBuckets(db, ListParams{Sort: "year", Order: "desc"})
	if err != nil {
		t.Fatalf("buckets desc: %v", err)
	}
	want = []IndexBucket{{"2020", 0}, {"2010", 2}, {"—", 3}}
	assertBuckets(t, got, want)
}

func TestIndexBucketsDispatch(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
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
