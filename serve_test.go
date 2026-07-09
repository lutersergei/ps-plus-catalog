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

func TestFragmentRendersOnlyCardsWithTotalHeader(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	for i := 1; i <= 30; i++ {
		id := fmt.Sprintf("g%02d", i)
		if err := store.UpsertGame(db, store.GameRow{ID: id, Title: fmt.Sprintf("G%02d", i)}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	tmpl, err := newIndexTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req := httptest.NewRequest("GET", "/?fragment=cards&offset=24&sort=title&order=asc", nil)
	rec := httptest.NewRecorder()

	handleIndex(rec, req, db, tmpl)

	body := rec.Body.String()
	if !strings.Contains(body, `class="gcard"`) || strings.Contains(body, "<form") || strings.Contains(body, "<head>") {
		t.Fatalf("фрагмент должен содержать только карточки, body[:200]=%q", body[:min(200, len(body))])
	}
	if got := rec.Header().Get("X-Total"); got != "30" {
		t.Fatalf("X-Total=%q, ждали 30", got)
	}
	// вторая партия начинается с глобального номера 24
	if !strings.Contains(body, `data-i="24"`) {
		t.Fatalf("нет data-i=24 в теле фрагмента")
	}
}

func TestFullPageCardsCarryDataIndex(t *testing.T) {
	tmpl, err := newIndexTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := pageData{
		Result: store.ListResult{
			Games:    []store.GameView{{ID: "g1", Title: "Game"}},
			Page:     3,
			PageSize: 24,
		},
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), `data-i="48"`) {
		t.Fatalf("карточка на странице 3 должна иметь data-i=48")
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

func TestHandleIndexParsesReviewCountFilterAndSort(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, game := range []store.GameRow{
		{ID: "g1", Title: "Few Reviews"},
		{ID: "g2", Title: "Enough Reviews"},
	} {
		if err := store.UpsertGame(db, game); err != nil {
			t.Fatalf("upsert %s: %v", game.ID, err)
		}
	}
	if _, err := db.Exec(`UPDATE games SET metacritic_user_count = 10, opencritic_player_count = 20 WHERE id = 'g1'`); err != nil {
		t.Fatalf("update g1: %v", err)
	}
	if _, err := db.Exec(`UPDATE games SET metacritic_user_count = 900, opencritic_player_count = 150 WHERE id = 'g2'`); err != nil {
		t.Fatalf("update g2: %v", err)
	}

	tmpl := template.Must(template.New("test").Parse(`total={{.Result.Total}} first={{(index .Result.Games 0).Title}} base={{.BaseQuery}} buckets={{len .Buckets}}`))
	req := httptest.NewRequest("GET", "/?reviews_from=1000&reviews_to=2000&sort=reviews&order=desc", nil)
	rec := httptest.NewRecorder()

	handleIndex(rec, req, db, tmpl)

	body := rec.Body.String()
	if !strings.Contains(body, "total=1") || !strings.Contains(body, "first=Enough Reviews") {
		t.Fatalf("body=%q, ждали одну игру с суммой оценок в диапазоне", body)
	}
	for _, want := range []string{"reviews_from=1000", "reviews_to=2000", "sort=reviews", "order=desc", "buckets=1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body=%q, ждали %q", body, want)
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
		`name="reviews_from"`,
		`name="reviews_to"`,
		`type="range"`,
		// сортировка по вердиктам
		`value="critic"`,
		`value="player"`,
		`value="reviews"`,
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

func TestIndexTemplateRendersSidebarFilterLayout(t *testing.T) {
	tmpl, err := newIndexTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	data := pageData{
		Result: store.ListResult{
			Games:    []store.GameView{{ID: "g1", Title: "Game"}},
			Page:     1,
			PageSize: 24,
		},
		Params: store.ListParams{Sort: "reviews", Order: "desc"},
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`class="filters app-shell"`,
		`class="filter-sidebar"`,
		`class="results-panel"`,
		`<aside`,
		`<main`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sidebar layout missing %q", want)
		}
	}
	if strings.Index(body, `class="filter-sidebar"`) > strings.Index(body, `class="results-panel"`) {
		t.Fatalf("filter sidebar should render before results panel")
	}
	if strings.Index(body, `name="sort"`) > strings.Index(body, `class="results-panel"`) {
		t.Fatalf("sort control should render inside filter sidebar")
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

func TestIndexTemplateRendersLetterIndexAndMoreLink(t *testing.T) {
	tmpl, err := newIndexTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := pageData{
		Result: store.ListResult{
			Games:      []store.GameView{{ID: "g1", Title: "Game"}},
			Total:      469,
			Page:       1,
			PageSize:   24,
			TotalPages: 20,
		},
		Params:     store.ListParams{Sort: "title"},
		BaseQuery:  template.URL("sort=title&order=asc"),
		Buckets:    []store.IndexBucket{{Label: "#", Offset: 0}, {Label: "A", Offset: 3}},
		NextOffset: 24,
		HasNext:    true,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`class="achip" data-offset="3"`,
		`id="moreLink"`,
		`offset=24`,
		`Показать ещё`,
		`id="shownCount"`,
		`data-next="24"`,
		`data-total="469"`,
		`data-start="0"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered template missing %q", want)
		}
	}
	if strings.Contains(body, `class="pager"`) {
		t.Fatalf("номерная пагинация должна быть удалена")
	}
}

func TestMoreLinkRenderedHiddenOnLastPage(t *testing.T) {
	tmpl, err := newIndexTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := pageData{
		Result: store.ListResult{
			Games:      []store.GameView{{ID: "g1", Title: "Game"}},
			Total:      25,
			Page:       2,
			PageSize:   24,
			TotalPages: 2,
		},
		BaseQuery:  template.URL("sort=title&order=asc"),
		NextOffset: 48,
		HasNext:    false,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()
	// ссылка должна существовать (прыжок по индексу может снова открыть середину
	// списка), но быть скрытой, пока дальше грузить нечего
	if !strings.Contains(body, `id="moreLink"`) {
		t.Fatalf("ссылка «Показать ещё» должна рендериться и на последней странице")
	}
	if !strings.Contains(body, `hidden`) {
		t.Fatalf("на последней странице ссылка должна быть hidden")
	}
}

func TestIndexTemplateHidesLetterIndexWithoutBuckets(t *testing.T) {
	tmpl, err := newIndexTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := pageData{
		Result: store.ListResult{Games: []store.GameView{{ID: "g1", Title: "Game"}}},
		Params: store.ListParams{Sort: "player"},
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(buf.String(), `class="achip"`) {
		t.Fatalf("индекс не должен рендериться без бакетов")
	}
}

func TestHandleIndexComputesBucketsPerSort(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := store.UpsertGame(db, store.GameRow{ID: "g1", Title: "Alpha", ReleaseYear: 2020}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := db.Exec(`UPDATE games SET player_average_score = 85 WHERE id = 'g1'`); err != nil {
		t.Fatalf("update: %v", err)
	}

	tmpl := template.Must(template.New("test").Parse(`buckets={{len .Buckets}}`))

	for _, tc := range []struct {
		sort string
		want string
	}{
		{"title", "buckets=1"},
		{"player", "buckets=1"},
		{"year", "buckets=1"},
		{"average", "buckets=0"}, // нет в UI — индекс не строится
	} {
		rec := httptest.NewRecorder()
		handleIndex(rec, httptest.NewRequest("GET", "/?sort="+tc.sort, nil), db, tmpl)
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("sort=%s: body=%q, ждали %s", tc.sort, rec.Body.String(), tc.want)
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
