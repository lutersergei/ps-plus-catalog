package scores

import (
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
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

func TestMetacriticScoresFallsBackToSearchCanonicalMatch(t *testing.T) {
	var pagePaths []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "www.metacritic.com":
			pagePaths = append(pagePaths, req.URL.Path)
			switch {
			case req.URL.Path == "/game/no-more-heroes-3/":
				return testHTTPResponse(http.StatusNotFound, ""), nil
			case strings.HasPrefix(req.URL.Path, "/search/"):
				return testHTTPResponse(http.StatusOK, `<a href="/game/rhythm-heaven-groove/">popular</a><a href="/game/no-more-heroes-iii/">No More Heroes III</a>`), nil
			case req.URL.Path == "/game/rhythm-heaven-groove/":
				return testHTTPResponse(http.StatusOK, metacriticTestPage("Rhythm Heaven Groove", 82)), nil
			case req.URL.Path == "/game/no-more-heroes-iii/":
				return testHTTPResponse(http.StatusOK, metacriticTestPage("No More Heroes III", 75)), nil
			default:
				t.Fatalf("unexpected metacritic path: %s", req.URL.Path)
			}
		case "backend.metacritic.com":
			if !strings.Contains(req.URL.Path, "/no-more-heroes-iii/") {
				t.Fatalf("unexpected user score path: %s", req.URL.Path)
			}
			return testHTTPResponse(http.StatusOK, `{"data":{"item":{"max":10,"score":7.7,"reviewCount":44}}}`), nil
		default:
			t.Fatalf("unexpected host: %s", req.URL.Host)
		}
		return nil, nil
	})}

	got, err := MetacriticScores(context.Background(), client, "No More Heroes 3")
	if err != nil {
		t.Fatalf("scores: %v", err)
	}
	if !got.Critic.Found || got.Critic.Score != 75 {
		t.Fatalf("critic=%+v, ждали score=75 found=true", got.Critic)
	}
	if !got.User.Found || got.User.Score != 77 || got.User.Count != 44 {
		t.Fatalf("user=%+v, ждали score=77 count=44 found=true", got.User)
	}
	if got.PageURL != "https://www.metacritic.com/game/no-more-heroes-iii/" {
		t.Fatalf("PageURL=%q", got.PageURL)
	}
	wantPaths := []string{"/game/no-more-heroes-3/", "/search/No More Heroes 3/", "/game/rhythm-heaven-groove/", "/game/no-more-heroes-iii/"}
	if strings.Join(pagePaths, "|") != strings.Join(wantPaths, "|") {
		t.Fatalf("page paths=%v, ждали %v", pagePaths, wantPaths)
	}
}

func TestMetacriticScoresRejectsSearchMismatch(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "www.metacritic.com":
			switch {
			case req.URL.Path == "/game/no-more-heroes-3/":
				return testHTTPResponse(http.StatusNotFound, ""), nil
			case strings.HasPrefix(req.URL.Path, "/search/"):
				return testHTTPResponse(http.StatusOK, `<a href="/game/rhythm-heaven-groove/">popular</a>`), nil
			case req.URL.Path == "/game/rhythm-heaven-groove/":
				return testHTTPResponse(http.StatusOK, metacriticTestPage("Rhythm Heaven Groove", 82)), nil
			default:
				t.Fatalf("unexpected metacritic path: %s", req.URL.Path)
			}
		case "backend.metacritic.com":
			return testHTTPResponse(http.StatusOK, `{"data":{"item":{"max":10,"score":9,"reviewCount":1}}}`), nil
		default:
			t.Fatalf("unexpected host: %s", req.URL.Host)
		}
		return nil, nil
	})}

	got, err := MetacriticScores(context.Background(), client, "No More Heroes 3")
	if err != nil {
		t.Fatalf("scores: %v", err)
	}
	if got.Critic.Found || got.User.Found || got.PageURL != "" {
		t.Fatalf("result=%+v, ждали пустой результат", got)
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

func metacriticTestPage(name string, score int) string {
	return `<script type="application/ld+json">{"@context":"https://schema.org","@type":"VideoGame","name":"` + name + `","aggregateRating":{"@type":"AggregateRating","name":"Metascore","ratingValue":` + strconv.Itoa(score) + `}}</script>`
}
