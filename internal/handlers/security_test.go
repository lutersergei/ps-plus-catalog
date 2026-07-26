package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

type browserStub struct{}

func (browserStub) Browse(context.Context, domain.ListParams, bool) (domain.BrowseResult, error) {
	return domain.BrowseResult{Result: domain.ListResult{Page: 1, PageSize: pageSize}}, nil
}

func TestCatalogHandlerSetsSecurityHeadersAndRestrictsMethods(t *testing.T) {
	handler, err := NewCatalogHandler(`total={{.Result.Total}}`, browserStub{}, nil)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := response.Header().Get(name); got != want {
			t.Errorf("%s=%q, ждали %q", name, got, want)
		}
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusMethodNotAllowed || !strings.Contains(response.Header().Get("Allow"), http.MethodGet) {
		t.Fatalf("POST status=%d Allow=%q", response.Code, response.Header().Get("Allow"))
	}
}

func TestExternalURLsRejectLookalikeHosts(t *testing.T) {
	malicious := "https://opencritic.com.evil.example/game/1"
	game := gameView{
		TitleEn:           "Game Name",
		OpenCriticPageURL: optionalString{String: malicious, Valid: true},
		MetacriticPageURL: optionalString{String: "https://www.metacritic.com.evil.example/game/x", Valid: true},
		HLTBPageURL:       optionalString{String: "https://howlongtobeat.com.evil.example/game/1", Valid: true},
	}
	if got := game.OpenCriticURL(); strings.Contains(got, "evil.example") || !strings.HasPrefix(got, "https://opencritic.com/search?") {
		t.Fatalf("OpenCritic URL=%q", got)
	}
	if got := game.MetacriticURL(); strings.Contains(got, "evil.example") || !strings.HasPrefix(got, "https://www.metacritic.com/search/") {
		t.Fatalf("Metacritic URL=%q", got)
	}
	if got := game.HLTBURL(); strings.Contains(got, "evil.example") || !strings.HasPrefix(got, "https://howlongtobeat.com/?q=") {
		t.Fatalf("HLTB URL=%q", got)
	}
}

func TestCatalogSourceAllowsOnlyKnownHosts(t *testing.T) {
	trusted := "https://blog.playstation.com/example"
	untrusted := "https://blog.playstation.com.evil.example/example"
	if got := trustedCatalogSource(&trusted); !got.Valid || got.String != trusted {
		t.Fatalf("trusted source=%+v", got)
	}
	if got := trustedCatalogSource(&untrusted); got.Valid {
		t.Fatalf("untrusted source=%+v", got)
	}
}

func TestBaseQueryEncodesUntrustedValuesBeforeMarkingURLSafe(t *testing.T) {
	malicious := `\"><script>alert('x')</script>&admin=1`
	raw := string(baseQuery(domain.ListParams{
		Search: malicious,
		Genres: []string{malicious},
		Sort:   "title",
		Order:  "asc",
	}))
	if strings.ContainsAny(raw, `<>"'`) {
		t.Fatalf("query содержит неэкранированные символы: %q", raw)
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if values.Get("q") != malicious || len(values["genre"]) != 1 || values["genre"][0] != malicious {
		t.Fatalf("значения не восстановились после кодирования: %#v", values)
	}
}
