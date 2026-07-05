package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"html/template"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lutersergei/ps-plus-catalog/internal/store"
)

func TestHandleIndexOffsetParamOverridesPage(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// 30 игр: G01..G30 — при pageSize 24 offset=24 начинает с G25.
	for i := 1; i <= 30; i++ {
		id := fmt.Sprintf("g%02d", i)
		title := fmt.Sprintf("G%02d", i)
		if err := store.UpsertGame(db, store.GameRow{ID: id, Title: title}); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	tmpl := template.Must(template.New("test").Parse(
		`first={{(index .Result.Games 0).Title}} page={{.Result.Page}}`))
	req := httptest.NewRequest("GET", "/?offset=24&page=1&sort=title&order=asc", nil)
	rec := httptest.NewRecorder()

	handleIndex(rec, req, db, tmpl)

	body := rec.Body.String()
	if !strings.Contains(body, "first=G25") || !strings.Contains(body, "page=2") {
		t.Fatalf("body=%q, ждали first=G25 page=2 (offset приоритетнее page)", body)
	}
}

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
	tmpl, err := newIndexTemplate()
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
				MetacriticUserCount:   sql.NullInt64{Int64: 1900, Valid: true},
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
		// слайдеры фильтров сохраняют старые GET-параметры
		`name="critic_from"`,
		`name="critic_to"`,
		`name="player_from"`,
		`name="player_to"`,
		`type="range"`,
		// сортировка по вердиктам
		`value="critic"`,
		`value="player"`,
		// пара вердиктов: классы цвета по величине оценки
		`class="chip good"`,
		// вес OpenCritic: глиф на чипе игроков и полупрозрачная оценка в источниках
		`◐`,
		`sv half`,
		// число голосов: сокращённое и точное
		`1,9к`,
		`>57<`,
		// переключатель темы и справка
		`id="themeToggle"`,
		`details class="help"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered template missing %q", want)
		}
	}
}

func TestIndexTemplateExcludedOCPlayerShowsDashWithVotes(t *testing.T) {
	tmpl, err := newIndexTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	data := pageData{
		Result: store.ListResult{
			Games: []store.GameView{{
				ID:                    "g1",
				Title:                 "Game",
				OpenCriticPlayer:      sql.NullInt64{Int64: 78, Valid: true},
				OpenCriticPlayerCount: sql.NullInt64{Int64: 11, Valid: true},
			}},
		},
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()
	// исключённая оценка не показывается числом, но число голосов видно
	if strings.Contains(body, `>78<`) {
		t.Fatalf("исключённая оценка OC не должна отображаться числом; body содержит >78<")
	}
	for _, want := range []string{`○`, `>11<`} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered template missing %q", want)
		}
	}
}

func TestIndexTemplateRendersPagerWindow(t *testing.T) {
	tmpl, err := newIndexTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	data := pageData{
		Result: store.ListResult{
			Games: []store.GameView{{
				ID:    "g1",
				Title: "Game",
			}},
			Page:       7,
			TotalPages: 20,
		},
		BaseQuery: template.URL("sort=title&order=asc"),
		Pages:     []int{3, 4, 5, 6, 7, 8, 9, 10, 11},
		HasPrev:   true,
		HasNext:   true,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`href="?sort=title&amp;order=asc&page=6">‹</a>`,
		`href="?sort=title&amp;order=asc&page=8">›</a>`,
		`class="cur">7</span>`,
		// окно не с первой страницы и не до последней — есть короткие ссылки на края
		`&page=1">1</a>`,
		`&page=20">20</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered template missing %q", want)
		}
	}
}

func TestScoreClass(t *testing.T) {
	cases := []struct {
		v    float64
		want string
	}{
		{90, "good"}, {75, "good"}, {74.9, "mid"}, {50, "mid"}, {49.9, "bad"}, {10, "bad"},
	}
	for _, c := range cases {
		if got := scoreClass(c.v); got != c.want {
			t.Errorf("scoreClass(%v) = %q, ждали %q", c.v, got, c.want)
		}
	}
}

func TestFmtCount(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"}, {57, "57"}, {999, "999"},
		{1000, "1к"}, {1100, "1,1к"}, {2340, "2,3к"}, {3400, "3,4к"}, {2960, "3к"},
	}
	for _, c := range cases {
		if got := fmtCount(c.n); got != c.want {
			t.Errorf("fmtCount(%d) = %q, ждали %q", c.n, got, c.want)
		}
	}
}

func TestHLTBBarScale(t *testing.T) {
	if got := hltbPct(42); got != 70 {
		t.Errorf("hltbPct(42) = %d, ждали 70", got)
	}
	if got := hltbPct(91); got != 100 {
		t.Errorf("hltbPct(91) = %d, ждали 100 (клампится к шкале)", got)
	}
	if got := hltbPct(0); got != 0 {
		t.Errorf("hltbPct(0) = %d, ждали 0", got)
	}
	if !hltbOver(91) {
		t.Error("hltbOver(91) = false, ждали true")
	}
	if hltbOver(60) {
		t.Error("hltbOver(60) = true, ждали false")
	}
}

func TestNormalizeSliderBoundsTreatsEdgeAsUnset(t *testing.T) {
	p := store.ListParams{CriticTo: 100, PlayerTo: 100, HLTBToHours: 80}
	normalizeSliderBounds(&p)
	if p.CriticTo != 0 || p.PlayerTo != 0 || p.HLTBToHours != 0 {
		t.Errorf("край слайдера должен означать «не задано»: %+v", p)
	}

	p = store.ListParams{CriticTo: 90, PlayerTo: 85, HLTBToHours: 40}
	normalizeSliderBounds(&p)
	if p.CriticTo != 90 || p.PlayerTo != 85 || p.HLTBToHours != 40 {
		t.Errorf("значения внутри шкалы должны сохраняться: %+v", p)
	}
}

func TestHandleIndexIgnoresSliderEdgeValues(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// Игра без оценок вовсе: фильтр critic_to=100 не должен её отсечь.
	if err := store.UpsertGame(db, store.GameRow{ID: "g1", Title: "No Scores"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	tmpl := template.Must(template.New("test").Parse(`total={{.Result.Total}} base={{.BaseQuery}}`))
	req := httptest.NewRequest("GET", "/?critic_from=0&critic_to=100&player_from=0&player_to=100&hltb_from=0&hltb_to=80", nil)
	rec := httptest.NewRecorder()

	handleIndex(rec, req, db, tmpl)

	body := rec.Body.String()
	if !strings.Contains(body, "total=1") {
		t.Fatalf("body=%q, ждали total=1 (края слайдеров = фильтр не задан)", body)
	}
	for _, absent := range []string{"critic_to=", "player_to=", "hltb_to="} {
		if strings.Contains(body, absent) {
			t.Fatalf("BaseQuery не должен содержать %q, body=%q", absent, body)
		}
	}
}
