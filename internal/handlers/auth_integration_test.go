package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lutersergei/ps-plus-catalog/internal/adapters/sqlite"
	"github.com/lutersergei/ps-plus-catalog/internal/domain"
	"github.com/lutersergei/ps-plus-catalog/internal/services"
)

type googleOAuthStub struct {
	exchangeCalls int
}

func (provider *googleOAuthStub) AuthorizationURL(state string) string {
	return "https://accounts.google.test/login?state=" + url.QueryEscape(state)
}

func (provider *googleOAuthStub) Exchange(_ context.Context, code string) (domain.GoogleIdentity, error) {
	provider.exchangeCalls++
	if code != "valid-code" {
		return domain.GoogleIdentity{}, fmt.Errorf("unexpected code %q", code)
	}
	return domain.GoogleIdentity{
		Subject: "google-subject", Email: "user@example.com", Name: "Тестовый пользователь",
		AvatarURL: "https://lh3.googleusercontent.com/avatar",
	}, nil
}

func TestGoogleLoginFavoriteListAndLogoutFlow(t *testing.T) {
	handler, accounts, db, provider := newAuthenticatedTestHandler(t)

	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/google/login?next=favorites", nil))
	if login.Code != http.StatusFound {
		t.Fatalf("login status=%d body=%q", login.Code, login.Body.String())
	}
	location, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse login location: %v", err)
	}
	state := location.Query().Get("state")
	if state == "" {
		t.Fatal("Google redirect не содержит state")
	}
	stateCookie := responseCookie(t, login, oauthStateCookie)
	nextCookie := responseCookie(t, login, oauthNextCookie)
	if stateCookie.Value != state || stateCookie.Path != "/games/auth/google" || !stateCookie.HttpOnly || !stateCookie.Secure {
		t.Fatalf("state cookie=%+v", stateCookie)
	}

	callbackRequest := httptest.NewRequest(
		http.MethodGet,
		"/auth/google/callback?state="+url.QueryEscape(state)+"&code=valid-code",
		nil,
	)
	callbackRequest.AddCookie(stateCookie)
	callbackRequest.AddCookie(nextCookie)
	callback := httptest.NewRecorder()
	handler.ServeHTTP(callback, callbackRequest)
	if callback.Code != http.StatusSeeOther || callback.Header().Get("Location") != "/games/favorites" {
		t.Fatalf("callback status=%d location=%q body=%q", callback.Code, callback.Header().Get("Location"), callback.Body.String())
	}
	if provider.exchangeCalls != 1 {
		t.Fatalf("exchange calls=%d", provider.exchangeCalls)
	}
	sessionCookie := responseCookie(t, callback, sessionCookieName)
	if sessionCookie.Path != "/games" || !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie=%+v", sessionCookie)
	}
	var storedHash string
	if err := db.QueryRow(`SELECT token_hash FROM user_sessions`).Scan(&storedHash); err != nil {
		t.Fatalf("read stored session: %v", err)
	}
	if len(storedHash) != 64 || storedHash == sessionCookie.Value {
		t.Fatalf("session token сохранён небезопасно: hash=%q token=%q", storedHash, sessionCookie.Value)
	}

	catalogRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	catalogRequest.AddCookie(sessionCookie)
	catalog := httptest.NewRecorder()
	handler.ServeHTTP(catalog, catalogRequest)
	if catalog.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%q", catalog.Code, catalog.Body.String())
	}
	for _, want := range []string{"Тестовый пользователь", `class="fav-toggle `, `aria-pressed="false"`, `/games/favorites`} {
		if !strings.Contains(catalog.Body.String(), want) {
			t.Fatalf("catalog body не содержит %q", want)
		}
	}

	withoutCSRF := httptest.NewRequest(http.MethodPost, "/api/favorite?game_id=g1", nil)
	withoutCSRF.AddCookie(sessionCookie)
	forbidden := httptest.NewRecorder()
	handler.ServeHTTP(forbidden, withoutCSRF)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("favorite without CSRF status=%d", forbidden.Code)
	}

	csrf := accounts.CSRFToken(sessionCookie.Value)
	addFavorite := httptest.NewRequest(http.MethodPost, "/api/favorite?game_id=g1", nil)
	addFavorite.AddCookie(sessionCookie)
	addFavorite.Header.Set("X-CSRF-Token", csrf)
	added := httptest.NewRecorder()
	handler.ServeHTTP(added, addFavorite)
	if added.Code != http.StatusOK || !strings.Contains(added.Body.String(), `"favorite":true`) {
		t.Fatalf("add favorite status=%d body=%q", added.Code, added.Body.String())
	}

	favoritesRequest := httptest.NewRequest(http.MethodGet, "/favorites", nil)
	favoritesRequest.AddCookie(sessionCookie)
	favorites := httptest.NewRecorder()
	handler.ServeHTTP(favorites, favoritesRequest)
	if favorites.Code != http.StatusOK || !strings.Contains(favorites.Body.String(), "Favorite Game") ||
		!strings.Contains(favorites.Body.String(), `aria-pressed="true"`) {
		t.Fatalf("favorites status=%d body=%q", favorites.Code, favorites.Body.String())
	}

	removeFavorite := httptest.NewRequest(http.MethodDelete, "/api/favorite?game_id=g1", nil)
	removeFavorite.AddCookie(sessionCookie)
	removeFavorite.Header.Set("X-CSRF-Token", csrf)
	removed := httptest.NewRecorder()
	handler.ServeHTTP(removed, removeFavorite)
	if removed.Code != http.StatusOK || !strings.Contains(removed.Body.String(), `"favorite":false`) {
		t.Fatalf("remove favorite status=%d body=%q", removed.Code, removed.Body.String())
	}

	emptyRequest := httptest.NewRequest(http.MethodGet, "/favorites", nil)
	emptyRequest.AddCookie(sessionCookie)
	empty := httptest.NewRecorder()
	handler.ServeHTTP(empty, emptyRequest)
	if empty.Code != http.StatusOK || !strings.Contains(empty.Body.String(), "В избранном ничего не найдено") {
		t.Fatalf("empty favorites status=%d body=%q", empty.Code, empty.Body.String())
	}

	logoutForm := url.Values{"csrf_token": {csrf}}
	logoutRequest := httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader(logoutForm.Encode()))
	logoutRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logoutRequest.AddCookie(sessionCookie)
	logout := httptest.NewRecorder()
	handler.ServeHTTP(logout, logoutRequest)
	if logout.Code != http.StatusSeeOther || logout.Header().Get("Location") != "/games/" {
		t.Fatalf("logout status=%d location=%q", logout.Code, logout.Header().Get("Location"))
	}
	if _, found, err := accounts.CurrentUser(context.Background(), sessionCookie.Value); err != nil || found {
		t.Fatalf("session after logout found=%v err=%v", found, err)
	}
}

func TestAuthRoutesRejectMissingSessionAndMismatchedState(t *testing.T) {
	handler, _, _, provider := newAuthenticatedTestHandler(t)
	favorites := httptest.NewRecorder()
	handler.ServeHTTP(favorites, httptest.NewRequest(http.MethodGet, "/favorites", nil))
	if favorites.Code != http.StatusSeeOther || favorites.Header().Get("Location") != "/games/auth/google/login?next=favorites" {
		t.Fatalf("favorites redirect status=%d location=%q", favorites.Code, favorites.Header().Get("Location"))
	}

	callbackRequest := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=wrong&code=valid-code", nil)
	callbackRequest.AddCookie(&http.Cookie{Name: oauthStateCookie, Value: "expected"})
	callback := httptest.NewRecorder()
	handler.ServeHTTP(callback, callbackRequest)
	if callback.Code != http.StatusBadRequest || provider.exchangeCalls != 0 {
		t.Fatalf("callback status=%d exchange calls=%d", callback.Code, provider.exchangeCalls)
	}
}

func newAuthenticatedTestHandler(
	t *testing.T,
) (*CatalogHandler, *services.AuthService, *sql.DB, *googleOAuthStub) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "auth-handler.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := upsertCatalogTestGame(db, domain.CatalogGame{ID: "g1", Title: "Favorite Game"}); err != nil {
		t.Fatalf("upsert test game: %v", err)
	}
	repository := sqlite.NewRepository(db)
	accounts := services.NewAuthService(repository, services.AuthOptions{})
	provider := &googleOAuthStub{}
	templateSource, err := os.ReadFile("../../templates/index.html")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := NewCatalogHandlerWithAuth(
		string(templateSource),
		services.NewCatalogService(repository),
		AuthConfig{Accounts: accounts, Google: provider, BasePath: "/games", SecureCookies: true},
		logger,
	)
	if err != nil {
		t.Fatalf("new authenticated handler: %v", err)
	}
	return handler, accounts, db, provider
}

func responseCookie(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name && cookie.MaxAge >= 0 {
			return cookie
		}
	}
	t.Fatalf("response не содержит cookie %s", name)
	return nil
}
