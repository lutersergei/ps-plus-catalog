package main

import (
	"html/template"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lutersergei/ps-plus-catalog/internal/store"
)

func TestHandleIndexParsesCriticAndPlayerFilters(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, game := range []store.GameRow{
		{ID: "g1", Title: "High Both"},
		{ID: "g2", Title: "Low Player"},
	} {
		if err := store.UpsertGame(db, game); err != nil {
			t.Fatalf("upsert %s: %v", game.ID, err)
		}
	}
	if _, err := db.Exec(`UPDATE games SET critic_average_score = 90, player_average_score = 85 WHERE id = 'g1'`); err != nil {
		t.Fatalf("update g1: %v", err)
	}
	if _, err := db.Exec(`UPDATE games SET critic_average_score = 90, player_average_score = 60 WHERE id = 'g2'`); err != nil {
		t.Fatalf("update g2: %v", err)
	}

	tmpl := template.Must(template.New("test").Parse(`total={{.Result.Total}} base={{.BaseQuery}}`))
	req := httptest.NewRequest("GET", "/?critic_from=80&player_from=80&sort=player&order=desc", nil)
	rec := httptest.NewRecorder()

	handleIndex(rec, req, db, tmpl)

	body := rec.Body.String()
	if !strings.Contains(body, "total=1") {
		t.Fatalf("body=%q, ждали total=1", body)
	}
	for _, want := range []string{"critic_from=80", "player_from=80", "sort=player", "order=desc"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body=%q, ждали %q в BaseQuery", body, want)
		}
	}
}
