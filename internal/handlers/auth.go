package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
	"github.com/lutersergei/ps-plus-catalog/internal/services"
)

const (
	sessionCookieName = "ps_extra_session"
	oauthStateCookie  = "ps_extra_oauth_state"
	oauthNextCookie   = "ps_extra_oauth_next"
	oauthStateTTL     = 10 * time.Minute
)

// AccountManager описывает операции локальной авторизации для HTTP-слоя.
type AccountManager interface {
	NewOAuthState() (string, error)
	SignIn(context.Context, domain.GoogleIdentity) (services.LoginSession, error)
	CurrentUser(context.Context, string) (domain.User, bool, error)
	SignOut(context.Context, string) error
	CSRFToken(string) string
	VerifyCSRF(string, string) bool
	SetFavorite(context.Context, int64, string, bool) error
}

// GoogleOAuth описывает внешнюю часть Google OAuth flow.
type GoogleOAuth interface {
	AuthorizationURL(string) string
	Exchange(context.Context, string) (domain.GoogleIdentity, error)
}

// AuthConfig включает Google OAuth для каталога. BasePath — внешний префикс
// reverse proxy, например /games; внутри контейнера маршруты остаются без него.
type AuthConfig struct {
	Accounts      AccountManager
	Google        GoogleOAuth
	BasePath      string
	SecureCookies bool
}

type currentAccount struct {
	User      domain.User
	Token     string
	CSRFToken string
}

func (h *CatalogHandler) handleGoogleLogin(response http.ResponseWriter, request *http.Request) {
	if !allowMethod(response, request, http.MethodGet) {
		return
	}
	state, err := h.accounts.NewOAuthState()
	if err != nil {
		h.logger.Error("не удалось создать OAuth state", "error", err)
		http.Error(response, "не удалось начать вход", http.StatusInternalServerError)
		return
	}
	h.setCookie(response, oauthStateCookie, state, h.oauthCookiePath(), time.Now().Add(oauthStateTTL))
	next := normalizeLoginNext(request.URL.Query().Get("next"))
	if next != "" {
		h.setCookie(response, oauthNextCookie, next, h.oauthCookiePath(), time.Now().Add(oauthStateTTL))
	} else {
		h.clearCookie(response, oauthNextCookie, h.oauthCookiePath())
	}
	response.Header().Set("Cache-Control", "no-store")
	http.Redirect(response, request, h.google.AuthorizationURL(state), http.StatusFound)
}

func (h *CatalogHandler) handleGoogleCallback(response http.ResponseWriter, request *http.Request) {
	if !allowMethod(response, request, http.MethodGet) {
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	stateCookie, cookieErr := request.Cookie(oauthStateCookie)
	state := request.URL.Query().Get("state")
	h.clearCookie(response, oauthStateCookie, h.oauthCookiePath())
	if request.URL.Query().Get("error") != "" {
		http.Error(response, "Google отклонил вход", http.StatusBadRequest)
		return
	}
	if cookieErr != nil || state == "" || len(state) > 128 ||
		subtle.ConstantTimeCompare([]byte(stateCookie.Value), []byte(state)) != 1 {
		http.Error(response, "OAuth state не совпадает или истёк", http.StatusBadRequest)
		return
	}
	identity, err := h.google.Exchange(request.Context(), request.URL.Query().Get("code"))
	if err != nil {
		h.logger.Warn("Google OAuth callback завершился ошибкой", "error", err)
		http.Error(response, "не удалось завершить вход через Google", http.StatusBadGateway)
		return
	}
	session, err := h.accounts.SignIn(request.Context(), identity)
	if err != nil {
		h.logger.Error("не удалось создать пользовательскую сессию", "error", err)
		http.Error(response, "не удалось создать сессию", http.StatusInternalServerError)
		return
	}
	h.setCookie(response, sessionCookieName, session.Token, h.cookiePath(), session.ExpiresAt)
	next := ""
	if cookie, err := request.Cookie(oauthNextCookie); err == nil {
		next = normalizeLoginNext(cookie.Value)
	}
	h.clearCookie(response, oauthNextCookie, h.oauthCookiePath())
	if next == "favorites" {
		http.Redirect(response, request, h.externalPath("/favorites"), http.StatusSeeOther)
		return
	}
	http.Redirect(response, request, h.externalPath("/"), http.StatusSeeOther)
}

func (h *CatalogHandler) handleLogout(response http.ResponseWriter, request *http.Request) {
	if !allowMethod(response, request, http.MethodPost) {
		return
	}
	account, found, err := h.currentAccount(response, request)
	if err != nil {
		http.Error(response, "внутренняя ошибка сервера", http.StatusInternalServerError)
		return
	}
	if found {
		if err := request.ParseForm(); err != nil {
			http.Error(response, "некорректная форма", http.StatusBadRequest)
			return
		}
		if !h.accounts.VerifyCSRF(account.Token, request.Form.Get("csrf_token")) {
			http.Error(response, "CSRF-токен не совпадает", http.StatusForbidden)
			return
		}
		if err := h.accounts.SignOut(request.Context(), account.Token); err != nil {
			h.logger.Error("не удалось завершить сессию", "error", err)
			http.Error(response, "не удалось завершить сессию", http.StatusInternalServerError)
			return
		}
	}
	h.clearCookie(response, sessionCookieName, h.cookiePath())
	http.Redirect(response, request, h.externalPath("/"), http.StatusSeeOther)
}

func (h *CatalogHandler) handleFavorite(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost && request.Method != http.MethodDelete {
		response.Header().Set("Allow", http.MethodPost+", "+http.MethodDelete)
		http.Error(response, "метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	account, found, err := h.currentAccount(response, request)
	if err != nil {
		http.Error(response, "внутренняя ошибка сервера", http.StatusInternalServerError)
		return
	}
	if !found {
		writeJSONError(response, "требуется вход", http.StatusUnauthorized)
		return
	}
	if !h.accounts.VerifyCSRF(account.Token, request.Header.Get("X-CSRF-Token")) {
		writeJSONError(response, "CSRF-токен не совпадает", http.StatusForbidden)
		return
	}
	favorite := request.Method == http.MethodPost
	err = h.accounts.SetFavorite(request.Context(), account.User.ID, request.URL.Query().Get("game_id"), favorite)
	switch {
	case errors.Is(err, services.ErrInvalidGameID):
		writeJSONError(response, "некорректный идентификатор игры", http.StatusBadRequest)
		return
	case errors.Is(err, domain.ErrGameNotFound):
		writeJSONError(response, "игра не найдена", http.StatusNotFound)
		return
	case err != nil:
		h.logger.Error("не удалось изменить избранное", "error", err)
		writeJSONError(response, "внутренняя ошибка сервера", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(response).Encode(map[string]bool{"favorite": favorite})
}

func (h *CatalogHandler) currentAccount(
	response http.ResponseWriter,
	request *http.Request,
) (currentAccount, bool, error) {
	if !h.authEnabled() {
		return currentAccount{}, false, nil
	}
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return currentAccount{}, false, nil
	}
	user, found, err := h.accounts.CurrentUser(request.Context(), cookie.Value)
	if err != nil {
		h.logger.Error("не удалось проверить пользовательскую сессию", "error", err)
		return currentAccount{}, false, err
	}
	if !found {
		h.clearCookie(response, sessionCookieName, h.cookiePath())
		return currentAccount{}, false, nil
	}
	return currentAccount{
		User:      user,
		Token:     cookie.Value,
		CSRFToken: h.accounts.CSRFToken(cookie.Value),
	}, true, nil
}

func (h *CatalogHandler) setCookie(
	response http.ResponseWriter,
	name, value, path string,
	expires time.Time,
) {
	maxAge := int(time.Until(expires).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(response, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *CatalogHandler) clearCookie(response http.ResponseWriter, name, path string) {
	http.SetCookie(response, &http.Cookie{
		Name: name, Value: "", Path: path,
		Expires: time.Unix(1, 0), MaxAge: -1,
		HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode,
	})
}

func (h *CatalogHandler) authEnabled() bool { return h.accounts != nil && h.google != nil }

func (h *CatalogHandler) cookiePath() string {
	if h.basePath == "" {
		return "/"
	}
	return h.basePath
}

func (h *CatalogHandler) oauthCookiePath() string {
	return h.externalPath("/auth/google")
}

func (h *CatalogHandler) externalPath(internal string) string {
	if internal == "/" {
		return h.basePath + "/"
	}
	return h.basePath + internal
}

func normalizeLoginNext(value string) string {
	value = strings.TrimSpace(value)
	if value == "favorites" || value == "/favorites" {
		return "favorites"
	}
	return ""
}

func allowMethod(response http.ResponseWriter, request *http.Request, allowed string) bool {
	if request.Method == allowed {
		return true
	}
	response.Header().Set("Allow", allowed)
	http.Error(response, "метод не поддерживается", http.StatusMethodNotAllowed)
	return false
}

func writeJSONError(response http.ResponseWriter, message string, status int) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]string{"error": message})
}

func validateAuthConfig(config AuthConfig) error {
	if (config.Accounts == nil) != (config.Google == nil) {
		return errors.New("accounts и Google OAuth должны быть настроены вместе")
	}
	if config.BasePath == "" {
		return nil
	}
	if !strings.HasPrefix(config.BasePath, "/") || strings.HasSuffix(config.BasePath, "/") ||
		strings.ContainsAny(config.BasePath, "?#\\") || strings.Contains(config.BasePath, "..") {
		return fmt.Errorf("некорректный внешний base path %q", config.BasePath)
	}
	return nil
}
