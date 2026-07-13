package main

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lutersergei/ps-plus-catalog/internal/store"
)

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
			t.Setenv("OPENCRITIC_API_KEYS", "")
			t.Setenv("OPENCRITIC_API_KEY", "")

			db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer db.Close()

			if err := store.UpsertGame(db, store.GameRow{
				ID: "g1", Title: "The Long Dark PS4 & PS5", TitleEn: "The Long Dark PS4 & PS5",
			}); err != nil {
				t.Fatalf("upsert: %v", err)
			}
			if err := store.UpdateMetacriticScores(
				db, "g1",
				sql.NullInt64{Int64: 77, Valid: true},
				sql.NullInt64{Int64: 81, Valid: true},
				sql.NullInt64{Int64: 42, Valid: true},
				sql.NullString{},
			); err != nil {
				t.Fatalf("seed metacritic: %v", err)
			}
			if err := store.SetSourceGenres(db, "g1", "metacritic", []store.SourceGenre{{Genre: "Adventure"}}); err != nil {
				t.Fatalf("seed genres: %v", err)
			}

			if err := syncScores(context.Background(), db, tt.client, 0, 30); err != nil {
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
			genres, err := store.SourceGenres(db, "g1")
			if err != nil {
				t.Fatalf("source genres: %v", err)
			}
			if got := strings.Join(genres["metacritic"], ","); got != "Adventure" {
				t.Fatalf("metacritic genres=%v, ждали [Adventure]", genres["metacritic"])
			}
		})
	}
}

type syncRoundTripFunc func(*http.Request) (*http.Response, error)

func (f syncRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func syncTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func metacriticPageForSyncTest(name string, score int) string {
	return `<script type="application/ld+json">{"@context":"https://schema.org","@type":"VideoGame","name":"` + name + `","aggregateRating":{"@type":"AggregateRating","name":"Metascore","ratingValue":` + strconv.Itoa(score) + `}}</script>`
}
