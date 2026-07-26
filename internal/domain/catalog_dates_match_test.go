package domain

import (
	"testing"
	"time"
)

func TestMatchCatalogAdditionNormalizesKnownStoreVariants(t *testing.T) {
	tests := []struct {
		name      string
		title     string
		titleEn   string
		candidate string
	}{
		{
			name:      "слитное Farcry и издание",
			title:     "FARCRY 6 Standard Edition PS4 & PS5",
			titleEn:   "FAR CRY 6 Standard Edition PS4 & PS5",
			candidate: "Far Cry 6",
		},
		{
			name:      "слитное название",
			title:     "FARCRY 6",
			titleEn:   "FARCRY 6",
			candidate: "Far Cry 6",
		},
		{
			name:      "суффикс подписки",
			title:     "Ghost of Tsushima DIRECTOR’S CUT (PlayStation Plus)",
			titleEn:   "Ghost of Tsushima DIRECTOR’S CUT (PlayStation Plus)",
			candidate: "Ghost Of Tsushima Director’s Cut",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			match, found, ambiguous := MatchCatalogAddition(test.title, test.titleEn, []CatalogAddition{{
				Title: test.candidate, AddedOn: matchDate("2026-07-21"),
			}})
			if !found || ambiguous || match.Title != test.candidate {
				t.Fatalf("match=%+v found=%v ambiguous=%v", match, found, ambiguous)
			}
		})
	}
}

func TestMatchCatalogAdditionChoosesLatestRepeat(t *testing.T) {
	match, found, ambiguous := MatchCatalogAddition("Far Cry 6", "Far Cry 6", []CatalogAddition{
		{Title: "Far Cry 6", AddedOn: matchDate("2023-01-17")},
		{Title: "Far Cry 6", AddedOn: matchDate("2026-07-21")},
	})
	if !found || ambiguous || match.AddedOn.Format("2006-01-02") != "2026-07-21" {
		t.Fatalf("match=%+v found=%v ambiguous=%v", match, found, ambiguous)
	}
}

func TestMatchCatalogAdditionDoesNotConfuseSequels(t *testing.T) {
	candidates := []CatalogAddition{
		{Title: "Cat Quest", AddedOn: matchDate("2024-07-16")},
		{Title: "Cat Quest II", AddedOn: matchDate("2024-07-16")},
		{Title: "Cat Quest III", AddedOn: matchDate("2025-08-19")},
	}
	match, found, ambiguous := MatchCatalogAddition("Cat Quest", "Cat Quest", candidates)
	if !found || ambiguous || match.Title != "Cat Quest" {
		t.Fatalf("match=%+v found=%v ambiguous=%v", match, found, ambiguous)
	}
	match, found, ambiguous = MatchCatalogAddition("Cat Quest", "Cat Quest", candidates[1:2])
	if found || ambiguous {
		t.Fatalf("продолжение ошибочно совпало: match=%+v found=%v ambiguous=%v", match, found, ambiguous)
	}
}

func matchDate(value string) time.Time {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		panic(err)
	}
	return parsed
}
