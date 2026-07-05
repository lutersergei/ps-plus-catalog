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
