package main

import (
	"bytes"
	"database/sql"
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

func TestIndexTemplateRendersCriticAndPlayerControls(t *testing.T) {
	tmpl, err := template.New("index").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
	}).Parse(indexHTML)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	data := pageData{
		Result: store.ListResult{
			Games: []store.GameView{{
				ID:                    "g1",
				Title:                 "Game",
				Metacritic:            sql.NullInt64{Int64: 80, Valid: true},
				MetacriticUser:        sql.NullInt64{Int64: 75, Valid: true},
				OpenCritic:            sql.NullInt64{Int64: 82, Valid: true},
				OpenCriticPlayer:      sql.NullInt64{Int64: 78, Valid: true},
				Average:               sql.NullFloat64{Float64: 79, Valid: true},
				CriticAverage:         sql.NullFloat64{Float64: 81, Valid: true},
				PlayerAverage:         sql.NullFloat64{Float64: 76.5, Valid: true},
				MetacriticUserCount:   sql.NullInt64{Int64: 120, Valid: true},
				OpenCriticPlayerCount: sql.NullInt64{Int64: 57, Valid: true},
			}},
		},
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`name="critic_from"`,
		`name="critic_to"`,
		`name="player_from"`,
		`name="player_to"`,
		`value="critic"`,
		`value="player"`,
		`MC critic`,
		`MC user`,
		`OC critic`,
		`OC player`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered template missing %q", want)
		}
	}
}
