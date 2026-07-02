package scores

import (
	"os"
	"testing"
)

func TestParseMetacriticUserStats(t *testing.T) {
	score, count, found, err := parseMetacriticUserStats([]byte(`{
		"data": {
			"item": {
				"max": 10,
				"score": 7.8,
				"reviewCount": 980
			}
		}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !found {
		t.Fatal("found=false, ждали true")
	}
	if score != 78 {
		t.Fatalf("score=%d, ждали 78", score)
	}
	if count != 980 {
		t.Fatalf("count=%d, ждали 980", count)
	}
}

func TestParseMetacriticUserStatsTreatsMissingScoreAsNoData(t *testing.T) {
	_, _, found, err := parseMetacriticUserStats([]byte(`{
		"data": {
			"item": {
				"max": 10,
				"score": null,
				"reviewCount": 0
			}
		}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if found {
		t.Fatal("found=true, ждали false")
	}
}

func TestMetacriticUserStatsURLUsesCanonicalSlugFromPage(t *testing.T) {
	html := []byte(`
		<script>
		"https://backend.metacritic.com/reviews/metacritic/user/games/bound-2016/stats/web?componentName=user-score-summary&componentDisplayName=User+Score+Summary&componentType=MetaScoreSummary"
		</script>
	`)
	got := metacriticUserStatsURL(html, "bound")
	want := "https://backend.metacritic.com/reviews/metacritic/user/games/bound-2016/stats/web?componentName=user-score-summary&componentDisplayName=User+Score+Summary&componentType=MetaScoreSummary"
	if got != want {
		t.Fatalf("url=%q, ждали %q", got, want)
	}
}

func TestParseMetacriticHollowKnightVoidheartCurrentPage(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/metacritic_hollow_knight_voidheart.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	score, found, err := parseMetacritic(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !found {
		t.Fatal("found=false, ждали true")
	}
	if score != 89 {
		t.Fatalf("score=%d, ждали 89", score)
	}
}

func TestParseMetacriticReadsNestedJSONLDGraph(t *testing.T) {
	raw := []byte(`<script type="application/ld+json">{
		"@graph": [
			{
				"@type": "VideoGame",
				"aggregateRating": {
					"name": "Metascore",
					"ratingValue": "89"
				}
			}
		]
	}</script>`)
	score, found, err := parseMetacritic(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !found {
		t.Fatal("found=false, ждали true")
	}
	if score != 89 {
		t.Fatalf("score=%d, ждали 89", score)
	}
}
