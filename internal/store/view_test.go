package store

import (
	"database/sql"
	"testing"
)

func TestOCPlayerWeightMirrorsSQLExpression(t *testing.T) {
	cases := []struct {
		name  string
		score sql.NullInt64
		count sql.NullInt64
		want  float64
	}{
		{"больше 100 голосов — полный вес", ni(80), ni(150), 1.0},
		{"ровно 101 голос — полный вес", ni(80), ni(101), 1.0},
		{"ровно 100 голосов — половинный вес", ni(80), ni(100), 0.5},
		{"20–100 голосов — половинный вес", ni(80), ni(57), 0.5},
		{"ровно 20 голосов — половинный вес", ni(80), ni(20), 0.5},
		{"меньше 20 голосов — не учитывается", ni(80), ni(19), 0},
		{"число голосов неизвестно — не учитывается", ni(80), sql.NullInt64{}, 0},
		{"оценки нет — не учитывается", sql.NullInt64{}, ni(150), 0},
		{"нулевая оценка — не учитывается", ni(0), ni(150), 0},
	}
	for _, c := range cases {
		g := GameView{OpenCriticPlayer: c.score, OpenCriticPlayerCount: c.count}
		if got := g.OCPlayerWeight(); got != c.want {
			t.Errorf("%s: OCPlayerWeight() = %v, ждали %v", c.name, got, c.want)
		}
	}
}

func TestOCWeightGlyph(t *testing.T) {
	cases := []struct {
		name  string
		score sql.NullInt64
		count sql.NullInt64
		want  string
	}{
		{"полный вес", ni(80), ni(150), "●"},
		{"половинный вес", ni(80), ni(57), "◐"},
		{"мало голосов — исключена", ni(80), ni(11), "○"},
		{"нет данных OpenCritic вовсе — без глифа", sql.NullInt64{}, sql.NullInt64{}, ""},
	}
	for _, c := range cases {
		g := GameView{OpenCriticPlayer: c.score, OpenCriticPlayerCount: c.count}
		if got := g.OCWeightGlyph(); got != c.want {
			t.Errorf("%s: OCWeightGlyph() = %q, ждали %q", c.name, got, c.want)
		}
	}
}

func ni(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }

func TestRuStoreURLSwitchesLocale(t *testing.T) {
	g := GameView{StoreURL: "https://store.playstation.com/tr-tr/concept/228903"}
	if got, want := g.RuStoreURL(), "https://store.playstation.com/ru-ru/concept/228903"; got != want {
		t.Errorf("RuStoreURL() = %q, ждали %q", got, want)
	}
	// URL без турецкой локали остаётся как есть
	g = GameView{StoreURL: "https://store.playstation.com/concept/1"}
	if got := g.RuStoreURL(); got != "https://store.playstation.com/concept/1" {
		t.Errorf("RuStoreURL() = %q, ждали без изменений", got)
	}
	// пустой URL — пустой
	if got := (GameView{}).RuStoreURL(); got != "" {
		t.Errorf("RuStoreURL() = %q, ждали пустую строку", got)
	}
}
