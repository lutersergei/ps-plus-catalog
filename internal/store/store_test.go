package store

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
)

// newTestDB открывает временную БД и наполняет её n играми (id g1..gN, active=1).
func newTestDB(t *testing.T, n int) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		id := "g" + strconv.Itoa(i)
		if err := UpsertGame(tx, GameRow{ID: id, Title: "Game " + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestListGames_PageClamp(t *testing.T) {
	db := newTestDB(t, 30) // 30 игр, pageSize 24 → 2 страницы
	res, err := ListGames(db, ListParams{Page: 9223372036854775807, PageSize: 24})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res.TotalPages != 2 {
		t.Fatalf("ждали 2 страницы, получили %d", res.TotalPages)
	}
	// Огромный page кламп­ится к последней странице, а не схлопывается в пустоту
	// из-за переполнения OFFSET.
	if res.Page != 2 {
		t.Errorf("page: ждали 2 (кламп), получили %d", res.Page)
	}
	if len(res.Games) != 6 { // 30 - 24
		t.Errorf("на 2-й странице ждали 6 игр, получили %d", len(res.Games))
	}
}

func TestNormalizeParams(t *testing.T) {
	p := ListParams{
		Page:     0,
		Search:   string(make([]byte, maxSearchLen+50)),
		Genres:   make([]string, maxGenres+10),
		YearFrom: 2020, YearTo: 2000, // перевёрнутый диапазон
	}
	NormalizeParams(&p)
	if p.Page != 1 {
		t.Errorf("page<1 → 1, получили %d", p.Page)
	}
	if len(p.Search) != maxSearchLen {
		t.Errorf("длина поиска: ждали %d, получили %d", maxSearchLen, len(p.Search))
	}
	if len(p.Genres) != maxGenres {
		t.Errorf("число жанров: ждали %d, получили %d", maxGenres, len(p.Genres))
	}
	if p.YearFrom != 0 || p.YearTo != 0 {
		t.Errorf("перевёрнутый диапазон годов должен обнулиться, получили %d..%d", p.YearFrom, p.YearTo)
	}
}

func TestDeactivateMissingAndCount(t *testing.T) {
	db := newTestDB(t, 5)
	if n, err := CountActive(db); err != nil || n != 5 {
		t.Fatalf("CountActive=%d err=%v, ждали 5", n, err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	// В снимок вошли только g1,g2,g3 → g4,g5 деактивируются.
	got, err := DeactivateMissing(tx, []string{"g1", "g2", "g3"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Errorf("деактивировано %d, ждали 2", got)
	}
	if n, _ := CountActive(db); n != 3 {
		t.Errorf("после деактивации активно %d, ждали 3", n)
	}
}
