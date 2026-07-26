package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

func TestListGamesTreatsFilterValuesAsSQLParameters(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repository := NewRepository(db)
	if _, err := repository.ApplyCatalogSnapshot(ctx, []domain.CatalogGame{{
		ID: "g1", Title: "Safe Game", Genres: []string{"Action"},
	}}, time.Now()); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	for _, params := range []domain.ListParams{
		{Search: `%' OR 1=1 --`, Page: 1, PageSize: 25},
		{Genres: []string{`Action') OR 1=1 --`}, Page: 1, PageSize: 25},
	} {
		result, err := repository.ListGames(ctx, params)
		if err != nil {
			t.Fatalf("list with untrusted filter: %v", err)
		}
		if result.Total != 0 {
			t.Fatalf("фильтр %#v вернул %d игр, ожидали 0", params, result.Total)
		}
	}
	active, err := repository.CountActive(ctx)
	if err != nil || active != 1 {
		t.Fatalf("таблица повреждена после запросов: active=%d err=%v", active, err)
	}
}
