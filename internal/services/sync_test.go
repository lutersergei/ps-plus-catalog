package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/adapters/psstore"
	"github.com/lutersergei/ps-plus-catalog/internal/adapters/scores"
	"github.com/lutersergei/ps-plus-catalog/internal/adapters/sqlite"
	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

type playStationStub struct {
	games []domain.CatalogGame
}

func (source playStationStub) FetchCatalog(context.Context) ([]domain.CatalogGame, error) {
	return append([]domain.CatalogGame(nil), source.games...), nil
}

func (playStationStub) FetchLanguages(context.Context, string) ([]string, []string, error) {
	return nil, nil, nil
}

type quotaOpenCriticStub struct{}

func (quotaOpenCriticStub) Enabled() bool { return true }
func (quotaOpenCriticStub) KeyCount() int { return 1 }
func (quotaOpenCriticStub) Lookup(context.Context, string) (domain.OpenCriticResult, error) {
	return domain.OpenCriticResult{}, domain.ErrProviderQuotaExhausted
}

func TestSyncScoresURLBackfillPreservesExistingRatings(t *testing.T) {
	tests := []struct {
		name    string
		client  *http.Client
		wantURL sql.NullString
	}{
		{
			name: "no matching page",
			client: &http.Client{Transport: syncRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "www.metacritic.com" {
					t.Fatalf("unexpected host: %s", req.URL.Host)
				}
				return syncTestResponse(http.StatusNotFound, ""), nil
			})},
		},
		{
			name: "user score request fails",
			client: &http.Client{Transport: syncRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Host {
				case "www.metacritic.com":
					switch req.URL.Path {
					case "/game/the-long-dark-ps4-ps5/":
						return syncTestResponse(http.StatusNotFound, ""), nil
					case "/game/the-long-dark/":
						return syncTestResponse(http.StatusOK, metacriticPageForSyncTest("The Long Dark", 77)), nil
					default:
						t.Fatalf("unexpected metacritic path: %s", req.URL.Path)
					}
				case "backend.metacritic.com":
					return syncTestResponse(http.StatusServiceUnavailable, ""), nil
				default:
					t.Fatalf("unexpected host: %s", req.URL.Host)
				}
				return nil, nil
			})},
			wantURL: sql.NullString{String: "https://www.metacritic.com/game/the-long-dark/", Valid: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer db.Close()

			repository := sqlite.NewRepository(db)
			if _, err := repository.ApplyCatalogSnapshot(ctx, []domain.CatalogGame{{
				ID: "g1", Title: "The Long Dark PS4 & PS5", TitleEn: "The Long Dark PS4 & PS5",
			}}, time.Now()); err != nil {
				t.Fatalf("snapshot: %v", err)
			}
			seedCritic, seedUser, seedUserCount := int64(77), int64(81), int64(42)
			if err := repository.UpdateMetacritic(ctx, "g1", domain.MetacriticUpdate{
				Critic: &seedCritic, User: &seedUser, UserCount: &seedUserCount,
			}); err != nil {
				t.Fatalf("seed metacritic: %v", err)
			}
			if err := repository.SetSourceGenres(ctx, "g1", "metacritic", []domain.SourceGenre{{Name: "Adventure"}}); err != nil {
				t.Fatalf("seed genres: %v", err)
			}

			service := NewSyncService(SyncDependencies{
				Repository: repository,
				Metacritic: scores.NewMetacriticClient(tt.client),
				Sleep:      func(context.Context, time.Duration) error { return nil },
			})
			if _, err := service.syncMetacritic(ctx, time.Now().AddDate(0, 0, -30)); err != nil {
				t.Fatalf("sync scores: %v", err)
			}

			var critic, user, userCount sql.NullInt64
			var pageURL sql.NullString
			if err := db.QueryRow(`
				SELECT metacritic_score, metacritic_user_score, metacritic_user_count, metacritic_url
				FROM games WHERE id = ?`, "g1").Scan(&critic, &user, &userCount, &pageURL); err != nil {
				t.Fatalf("read metacritic: %v", err)
			}
			if !critic.Valid || critic.Int64 != 77 || !user.Valid || user.Int64 != 81 || !userCount.Valid || userCount.Int64 != 42 {
				t.Fatalf("ratings after URL backfill: critic=%v user=%v userCount=%v", critic, user, userCount)
			}
			if pageURL != tt.wantURL {
				t.Fatalf("metacritic_url=%v, ждали %v", pageURL, tt.wantURL)
			}
			var genre string
			if err := db.QueryRow(`SELECT genre FROM game_source_genres WHERE game_id = 'g1' AND source = 'metacritic'`).Scan(&genre); err != nil {
				t.Fatalf("source genre: %v", err)
			}
			if genre != "Adventure" {
				t.Fatalf("metacritic genre=%q, ждали Adventure", genre)
			}
		})
	}
}

func TestSyncCatalogRecordsMembershipPeriods(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	snapshots := []string{
		`[{"catalogKey":"A","count":2,"games":[
			{"productId":"g1","name":"Game 1"},
			{"productId":"g2","name":"Game 2"}
		]}]`,
		`[{"catalogKey":"A","count":2,"games":[
			{"productId":"g2","name":"Game 2"},
			{"productId":"g3","name":"Game 3"}
		]}]`,
	}
	request := 0
	client := &http.Client{Transport: syncRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "www.playstation.com" {
			t.Fatalf("unexpected host: %s", req.URL.Host)
		}
		if request >= len(snapshots) {
			t.Fatalf("unexpected catalog request %d", request+1)
		}
		body := snapshots[request]
		request++
		return syncTestResponse(http.StatusOK, body), nil
	})}

	repository := sqlite.NewRepository(db)
	service := NewSyncService(SyncDependencies{
		Repository:  repository,
		PlayStation: psstore.NewClient(client),
	})
	if _, err := service.Run(ctx, SyncOptions{SkipScores: true}); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	var initialUnknown int
	if err := db.QueryRow(`
SELECT COUNT(*) FROM catalog_memberships
WHERE removed_on IS NULL AND added_on IS NULL`).Scan(&initialUnknown); err != nil {
		t.Fatalf("read initial periods: %v", err)
	}
	if initialUnknown != 2 {
		t.Fatalf("исходных периодов без придуманной даты=%d, ждали 2", initialUnknown)
	}

	if _, err := service.Run(ctx, SyncOptions{SkipScores: true}); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	var g1Closed, g3Observed int
	if err := db.QueryRow(`
SELECT
  EXISTS(SELECT 1 FROM catalog_memberships WHERE game_id = 'g1' AND removed_on IS NOT NULL),
  EXISTS(SELECT 1 FROM catalog_memberships
         WHERE game_id = 'g3' AND removed_on IS NULL
           AND added_on IS NOT NULL AND added_on_source = 'observed')
`).Scan(&g1Closed, &g3Observed); err != nil {
		t.Fatalf("read changed periods: %v", err)
	}
	if g1Closed != 1 || g3Observed != 1 {
		t.Fatalf("g1Closed=%d g3Observed=%d, ждали 1/1", g1Closed, g3Observed)
	}
}

func TestSyncServiceProtectsAgainstSuspiciousCatalogShrink(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repository := sqlite.NewRepository(db)
	initial := catalogGames(100)
	if _, err := repository.ApplyCatalogSnapshot(ctx, initial, time.Now()); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	service := NewSyncService(SyncDependencies{
		Repository:  repository,
		PlayStation: playStationStub{games: catalogGames(50)},
		Now:         time.Now,
	})
	if _, err := service.Run(ctx, SyncOptions{SkipScores: true}); err == nil || !strings.Contains(err.Error(), "подозрительно мал") {
		t.Fatalf("err=%v, ждали защиту от сжатия", err)
	}
	if active, err := repository.CountActive(ctx); err != nil || active != 100 {
		t.Fatalf("active=%d err=%v, снимок не должен применяться", active, err)
	}
	if _, err := service.Run(ctx, SyncOptions{SkipScores: true, AllowShrink: true}); err != nil {
		t.Fatalf("allow shrink: %v", err)
	}
	if active, err := repository.CountActive(ctx); err != nil || active != 50 {
		t.Fatalf("active=%d err=%v, ждали 50", active, err)
	}
}

func TestSyncOpenCriticStopsOnQuotaWithoutMarkingGameChecked(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repository := sqlite.NewRepository(db)
	if _, err := repository.ApplyCatalogSnapshot(ctx, catalogGames(1), time.Now()); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	service := NewSyncService(SyncDependencies{Repository: repository, OpenCritic: quotaOpenCriticStub{}})
	processed, exhausted, err := service.syncOpenCritic(ctx, time.Now(), 25)
	if err != nil || !exhausted || processed != 0 {
		t.Fatalf("processed=%d exhausted=%v err=%v", processed, exhausted, err)
	}
	var checkedAt sql.NullTime
	if err := db.QueryRow(`SELECT oc_checked_at FROM games WHERE id = 'g000'`).Scan(&checkedAt); err != nil {
		t.Fatalf("read checked_at: %v", err)
	}
	if checkedAt.Valid {
		t.Fatalf("квотная ошибка не должна помечать игру проверенной: %v", checkedAt.Time)
	}
}

func TestProviderRunLimitHandlesUnlimitedAndOverflow(t *testing.T) {
	if got := providerRunLimit(0, 3); got != 0 {
		t.Fatalf("unlimited=%d", got)
	}
	maximum := int(^uint(0) >> 1)
	if got := providerRunLimit(maximum, 2); got != maximum {
		t.Fatalf("overflow limit=%d, want %d", got, maximum)
	}
}

func TestQuotaErrorRemainsDetectableWhenWrapped(t *testing.T) {
	if !errors.Is(fmt.Errorf("provider: %w", domain.ErrProviderQuotaExhausted), domain.ErrProviderQuotaExhausted) {
		t.Fatal("ошибка квоты должна поддерживать errors.Is")
	}
}

func catalogGames(count int) []domain.CatalogGame {
	games := make([]domain.CatalogGame, count)
	for i := range games {
		games[i] = domain.CatalogGame{
			ID:      fmt.Sprintf("g%03d", i),
			Title:   fmt.Sprintf("Game %03d", i),
			TitleEn: fmt.Sprintf("Game %03d", i),
		}
	}
	return games
}

func metacriticPageForSyncTest(name string, score int) string {
	return `<script type="application/ld+json">{"@context":"https://schema.org","@type":"VideoGame","name":"` + name + `","aggregateRating":{"@type":"AggregateRating","name":"Metascore","ratingValue":` + strconv.Itoa(score) + `}}</script>`
}
