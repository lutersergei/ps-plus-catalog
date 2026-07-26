package handlers

import (
	"testing"
	"time"
)

func TestOCPlayerWeightMirrorsSQLExpression(t *testing.T) {
	cases := []struct {
		name  string
		score optionalInt
		count optionalInt
		want  float64
	}{
		{"больше 100 голосов — полный вес", ni(80), ni(150), 1.0},
		{"ровно 101 голос — полный вес", ni(80), ni(101), 1.0},
		{"ровно 100 голосов — половинный вес", ni(80), ni(100), 0.5},
		{"20–100 голосов — половинный вес", ni(80), ni(57), 0.5},
		{"ровно 20 голосов — половинный вес", ni(80), ni(20), 0.5},
		{"меньше 20 голосов — не учитывается", ni(80), ni(19), 0},
		{"число голосов неизвестно — не учитывается", ni(80), optionalInt{}, 0},
		{"оценки нет — не учитывается", optionalInt{}, ni(150), 0},
		{"нулевая оценка — не учитывается", ni(0), ni(150), 0},
	}
	for _, c := range cases {
		g := gameView{OpenCriticPlayer: c.score, OpenCriticPlayerCount: c.count}
		if got := g.OCPlayerWeight(); got != c.want {
			t.Errorf("%s: OCPlayerWeight() = %v, ждали %v", c.name, got, c.want)
		}
	}
}

func TestSavedProviderURLsAreUsedDirectly(t *testing.T) {
	game := gameView{
		TitleEn:           "Example",
		MetacriticPageURL: optionalString{String: "https://www.metacritic.com/game/example/", Valid: true},
		OpenCriticPageURL: optionalString{String: "https://opencritic.com/game/1/example", Valid: true},
		HLTBPageURL:       optionalString{String: "https://howlongtobeat.com/game/1", Valid: true},
	}
	if got := game.MetacriticURL(); got != game.MetacriticPageURL.String {
		t.Fatalf("MetacriticURL=%q", got)
	}
	if got := game.OpenCriticURL(); got != game.OpenCriticPageURL.String {
		t.Fatalf("OpenCriticURL=%q", got)
	}
	if got := game.HLTBURL(); got != game.HLTBPageURL.String {
		t.Fatalf("HLTBURL=%q", got)
	}
}

func TestMetacriticURLFallsBackToEnglishSearch(t *testing.T) {
	game := gameView{Title: "Локальное имя", TitleEn: "Game™ Name"}
	if got := game.MetacriticURL(); got != "https://www.metacritic.com/search/Game%20Name/" {
		t.Fatalf("MetacriticURL=%q", got)
	}
}

func TestCatalogAddedShortLabel(t *testing.T) {
	if got := (gameView{}).CatalogAddedShortLabel(); got != "" {
		t.Fatalf("empty label=%q", got)
	}
	game := gameView{CatalogAddedOn: optionalTime{
		Time: time.Date(2022, 6, 23, 0, 0, 0, 0, time.UTC), Valid: true,
	}}
	if got := game.CatalogAddedShortLabel(); got != "23.06.22" {
		t.Fatalf("label=%q", got)
	}
}

func TestOCWeightGlyph(t *testing.T) {
	cases := []struct {
		name  string
		score optionalInt
		count optionalInt
		want  string
	}{
		{"полный вес", ni(80), ni(150), "●"},
		{"половинный вес", ni(80), ni(57), "◐"},
		{"мало голосов — исключена", ni(80), ni(11), "○"},
		{"нет данных OpenCritic вовсе — без глифа", optionalInt{}, optionalInt{}, ""},
	}
	for _, c := range cases {
		g := gameView{OpenCriticPlayer: c.score, OpenCriticPlayerCount: c.count}
		if got := g.OCWeightGlyph(); got != c.want {
			t.Errorf("%s: OCWeightGlyph() = %q, ждали %q", c.name, got, c.want)
		}
	}
}

func ni(v int64) optionalInt { return optionalInt{Int64: v, Valid: true} }

func TestRuStoreURLSwitchesLocale(t *testing.T) {
	g := gameView{StoreURL: "https://store.playstation.com/tr-tr/concept/228903"}
	if got, want := g.RuStoreURL(), "https://store.playstation.com/ru-ua/concept/228903"; got != want {
		t.Errorf("RuStoreURL() = %q, ждали %q", got, want)
	}
	// URL без турецкой локали остаётся как есть
	g = gameView{StoreURL: "https://store.playstation.com/concept/1"}
	if got := g.RuStoreURL(); got != "https://store.playstation.com/concept/1" {
		t.Errorf("RuStoreURL() = %q, ждали без изменений", got)
	}
	// пустой URL — пустой
	if got := (gameView{}).RuStoreURL(); got != "" {
		t.Errorf("RuStoreURL() = %q, ждали пустую строку", got)
	}
}
