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

func TestTitleLetterBucketsCumulativeOffsets(t *testing.T) {
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

	got, err := TitleLetterBuckets(db, ListParams{Sort: "title", Order: "asc"})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	want := []LetterBucket{{"#", 0}, {"A", 1}, {"B", 3}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, ждали %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, ждали %+v", got, want)
		}
	}
}

func TestTitleLetterBucketsDescReversesOrder(t *testing.T) {
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

	got, err := TitleLetterBuckets(db, ListParams{Sort: "title", Order: "desc"})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	want := []LetterBucket{{"B", 0}, {"A", 1}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, ждали %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, ждали %+v", got, want)
		}
	}
}

func TestTitleLetterBucketsRespectsFilters(t *testing.T) {
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

	got, err := TitleLetterBuckets(db, ListParams{Sort: "title", Order: "asc", YearFrom: 2015})
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	want := []LetterBucket{{"A", 0}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %+v, ждали %+v (фильтр по году должен применяться)", got, want)
	}
}
