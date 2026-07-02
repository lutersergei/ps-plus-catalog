package scores

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
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

func TestMetacriticScoresTriesRawSlugBeforeCleanedTitle(t *testing.T) {
	page, err := os.ReadFile("../../testdata/metacritic_hollow_knight_voidheart.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var pagePaths []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "www.metacritic.com":
			pagePaths = append(pagePaths, req.URL.Path)
			switch req.URL.Path {
			case "/game/hollow-knight-voidheart-edition/":
				return testHTTPResponse(http.StatusOK, string(page)), nil
			case "/game/hollow-knight-voidheart/":
				return testHTTPResponse(http.StatusNotFound, ""), nil
			default:
				t.Fatalf("unexpected metacritic path: %s", req.URL.Path)
			}
		case "backend.metacritic.com":
			if !strings.Contains(req.URL.Path, "/hollow-knight-voidheart-edition/") {
				t.Fatalf("unexpected user score path: %s", req.URL.Path)
			}
			return testHTTPResponse(http.StatusOK, `{"data":{"item":{"max":10,"score":9,"reviewCount":751}}}`), nil
		default:
			t.Fatalf("unexpected host: %s", req.URL.Host)
		}
		return nil, nil
	})}

	got, err := MetacriticScores(context.Background(), client, "Hollow Knight Voidheart Edition")
	if err != nil {
		t.Fatalf("scores: %v", err)
	}
	if !got.Critic.Found || got.Critic.Score != 89 {
		t.Fatalf("critic=%+v, ждали score=89 found=true", got.Critic)
	}
	if !got.User.Found || got.User.Score != 90 || got.User.Count != 751 {
		t.Fatalf("user=%+v, ждали score=90 count=751 found=true", got.User)
	}
	if len(pagePaths) != 1 || pagePaths[0] != "/game/hollow-knight-voidheart-edition/" {
		t.Fatalf("page paths=%v, ждали только raw slug", pagePaths)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
