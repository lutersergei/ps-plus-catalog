package psstore

import (
	"os"
	"strings"
	"testing"
)

func TestParseGamesList_ProductIDUnique(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/gameslist_full.json")
	if err != nil {
		t.Fatal(err)
	}
	games, err := parseGamesList(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(games) == 0 {
		t.Fatal("parseGamesList вернул пустой список")
	}
	seen := make(map[string]string, len(games)) // productId → title
	for _, g := range games {
		if g.ID == "" {
			t.Errorf("пустой productId у игры %q", g.Title)
			continue
		}
		if prev, dup := seen[g.ID]; dup {
			t.Errorf("дублирующийся productId %q: %q и %q", g.ID, prev, g.Title)
		}
		seen[g.ID] = g.Title
	}
}

func TestParseGamesList_Valid(t *testing.T) {
	raw := `[{"catalogKey":"A","count":2,"games":[
		{"productId":"P1","name":"Alpha","nameEn":"Alpha","releaseDate":"2020-01-02T00:00:00Z","genre":["ACTION","ACTION"]},
		{"productId":"P2","name":"Beta","nameEn":"Beta"}]}]`
	games, err := parseGamesList([]byte(raw))
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if len(games) != 2 {
		t.Fatalf("ждали 2 игры, получили %d", len(games))
	}
	if games[0].ReleaseYear != 2020 {
		t.Errorf("год: ждали 2020, получили %d", games[0].ReleaseYear)
	}
	if len(games[0].Genres) != 1 { // дубль ACTION схлопнут
		t.Errorf("жанры: ждали 1 (дедуп), получили %v", games[0].Genres)
	}
}

func TestParseGamesList_Invalid(t *testing.T) {
	cases := []struct {
		name, raw, wantSubstr string
	}{
		{"пустой ответ", `[]`, "пустой ответ"},
		{"пустой productId", `[{"catalogKey":"A","count":1,"games":[{"productId":"","name":"X"}]}]`, "пустой productId"},
		{"пустой name", `[{"catalogKey":"A","count":1,"games":[{"productId":"P1","name":""}]}]`, "пустой name"},
		{"дубль productId", `[{"catalogKey":"A","count":2,"games":[{"productId":"P1","name":"A"},{"productId":"P1","name":"B"}]}]`, "дублирующийся productId"},
		{"count не сходится (частичный ответ)", `[{"catalogKey":"A","count":5,"games":[{"productId":"P1","name":"A"}]}]`, "count"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseGamesList([]byte(c.raw))
			if err == nil {
				t.Fatalf("ждали ошибку, получили nil")
			}
			if !strings.Contains(err.Error(), c.wantSubstr) {
				t.Errorf("ошибка %q не содержит %q", err.Error(), c.wantSubstr)
			}
		})
	}
}
