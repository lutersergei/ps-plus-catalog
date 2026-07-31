// Package googleauth реализует минимальный OAuth 2.0/OpenID Connect клиент
// Google. Access token используется только для запроса userinfo и не хранится.
package googleauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

const maxResponseBytes = 1 << 20

// Endpoints позволяет тестам использовать локальный OAuth-сервер.
type Endpoints struct {
	AuthorizationURL string
	TokenURL         string
	UserInfoURL      string
}

// GoogleEndpoints — публичные OAuth/OpenID Connect endpoints Google.
var GoogleEndpoints = Endpoints{
	AuthorizationURL: "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL:         "https://oauth2.googleapis.com/token",
	UserInfoURL:      "https://openidconnect.googleapis.com/v1/userinfo",
}

// Config содержит OAuth credentials и точный callback URL приложения.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Endpoints    Endpoints
	HTTPClient   *http.Client
}

// Client выполняет Google OAuth flow.
type Client struct {
	clientID     string
	clientSecret string
	redirectURL  string
	endpoints    Endpoints
	httpClient   *http.Client
}

// New проверяет конфигурацию и создаёт Google OAuth клиент.
func New(config Config) (*Client, error) {
	config.ClientID = strings.TrimSpace(config.ClientID)
	config.ClientSecret = strings.TrimSpace(config.ClientSecret)
	config.RedirectURL = strings.TrimSpace(config.RedirectURL)
	if config.ClientID == "" || config.ClientSecret == "" || config.RedirectURL == "" {
		return nil, errors.New("Google OAuth: client id, client secret и redirect URL обязательны")
	}
	if config.Endpoints == (Endpoints{}) {
		config.Endpoints = GoogleEndpoints
	}
	for name, raw := range map[string]string{
		"authorization": config.Endpoints.AuthorizationURL,
		"token":         config.Endpoints.TokenURL,
		"userinfo":      config.Endpoints.UserInfoURL,
	} {
		if err := validateEndpoint(raw); err != nil {
			return nil, fmt.Errorf("Google OAuth %s endpoint: %w", name, err)
		}
	}
	redirect, err := url.Parse(config.RedirectURL)
	if err != nil || redirect.RawQuery != "" || redirect.Fragment != "" {
		return nil, errors.New("Google OAuth redirect URL некорректен")
	}
	if err := validateEndpoint(config.RedirectURL); err != nil {
		return nil, fmt.Errorf("Google OAuth redirect URL: %w", err)
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		clientID:     config.ClientID,
		clientSecret: config.ClientSecret,
		redirectURL:  config.RedirectURL,
		endpoints:    config.Endpoints,
		httpClient:   httpClient,
	}, nil
}

// AuthorizationURL возвращает URL входа Google со state и минимальными scopes.
func (c *Client) AuthorizationURL(state string) string {
	parsed, _ := url.Parse(c.endpoints.AuthorizationURL)
	query := parsed.Query()
	query.Set("client_id", c.clientID)
	query.Set("redirect_uri", c.redirectURL)
	query.Set("response_type", "code")
	query.Set("scope", "openid email profile")
	query.Set("state", state)
	query.Set("prompt", "select_account")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// Exchange меняет одноразовый code на access token, запрашивает userinfo и
// возвращает только данные, необходимые локальной учётной записи.
func (c *Client) Exchange(ctx context.Context, code string) (domain.GoogleIdentity, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 4096 {
		return domain.GoogleIdentity{}, errors.New("Google OAuth: некорректный authorization code")
	}
	accessToken, err := c.exchangeToken(ctx, code)
	if err != nil {
		return domain.GoogleIdentity{}, err
	}
	return c.userInfo(ctx, accessToken)
}

func (c *Client) exchangeToken(ctx context.Context, code string) (string, error) {
	form := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {c.redirectURL},
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoints.TokenURL,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("создать token request Google: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("получить token Google: %w", err)
	}
	defer response.Body.Close()

	var payload struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := decodeJSON(response.Body, &payload); err != nil {
		return "", fmt.Errorf("разобрать token response Google: %w", err)
	}
	if response.StatusCode != http.StatusOK || payload.Error != "" {
		return "", fmt.Errorf("Google OAuth token endpoint: status=%d error=%q", response.StatusCode, payload.Error)
	}
	if payload.AccessToken == "" {
		return "", errors.New("Google OAuth token endpoint не вернул access token")
	}
	return payload.AccessToken, nil
}

func (c *Client) userInfo(ctx context.Context, accessToken string) (domain.GoogleIdentity, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoints.UserInfoURL, nil)
	if err != nil {
		return domain.GoogleIdentity{}, fmt.Errorf("создать userinfo request Google: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return domain.GoogleIdentity{}, fmt.Errorf("получить userinfo Google: %w", err)
	}
	defer response.Body.Close()

	var payload struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := decodeJSON(response.Body, &payload); err != nil {
		return domain.GoogleIdentity{}, fmt.Errorf("разобрать userinfo Google: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return domain.GoogleIdentity{}, fmt.Errorf("Google userinfo endpoint: status=%d", response.StatusCode)
	}
	payload.Subject = strings.TrimSpace(payload.Subject)
	payload.Email = strings.TrimSpace(payload.Email)
	if payload.Subject == "" || payload.Email == "" || !payload.EmailVerified {
		return domain.GoogleIdentity{}, errors.New("Google userinfo не содержит подтверждённый email или subject")
	}
	return domain.GoogleIdentity{
		Subject:   payload.Subject,
		Email:     payload.Email,
		Name:      strings.TrimSpace(payload.Name),
		AvatarURL: trustedAvatarURL(payload.Picture),
	}, nil
}

func decodeJSON(reader io.Reader, target any) error {
	raw, err := io.ReadAll(io.LimitReader(reader, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxResponseBytes {
		return errors.New("ответ превышает допустимый размер")
	}
	return json.Unmarshal(raw, target)
}

func validateEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return errors.New("некорректный URL")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost") {
		return nil
	}
	return errors.New("разрешён только HTTPS или loopback HTTP для тестов")
}

func trustedAvatarURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "googleusercontent.com" && !strings.HasSuffix(host, ".googleusercontent.com") {
		return ""
	}
	return parsed.String()
}
