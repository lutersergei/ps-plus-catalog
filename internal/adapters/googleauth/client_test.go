package googleauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClientCompletesOAuthFlow(t *testing.T) {
	var tokenCalls, userInfoCalls int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			tokenCalls++
			if request.Method != http.MethodPost {
				t.Fatalf("token method=%s", request.Method)
			}
			if err := request.ParseForm(); err != nil {
				t.Fatalf("parse token form: %v", err)
			}
			for key, want := range map[string]string{
				"client_id": "client-id", "client_secret": "client-secret",
				"code": "valid-code", "grant_type": "authorization_code",
				"redirect_uri": "http://localhost:8080/auth/google/callback",
			} {
				if got := request.Form.Get(key); got != want {
					t.Fatalf("token form %s=%q, ожидали %q", key, got, want)
				}
			}
			response.Header().Set("Content-Type", "application/json")
			fmt.Fprint(response, `{"access_token":"access-secret","token_type":"Bearer"}`)
		case "/userinfo":
			userInfoCalls++
			if got := request.Header.Get("Authorization"); got != "Bearer access-secret" {
				t.Fatalf("Authorization=%q", got)
			}
			response.Header().Set("Content-Type", "application/json")
			fmt.Fprint(response, `{
                    "sub":"google-subject",
                    "email":"user@example.com",
                    "email_verified":true,
                    "name":"Test User",
                    "picture":"https://lh3.googleusercontent.com/avatar"
                }`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	client, err := New(Config{
		ClientID: "client-id", ClientSecret: "client-secret",
		RedirectURL: "http://localhost:8080/auth/google/callback",
		Endpoints: Endpoints{
			AuthorizationURL: server.URL + "/auth",
			TokenURL:         server.URL + "/token", UserInfoURL: server.URL + "/userinfo",
		},
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	authorizationURL, err := url.Parse(client.AuthorizationURL("state-value"))
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	if authorizationURL.Query().Get("state") != "state-value" ||
		authorizationURL.Query().Get("scope") != "openid email profile" ||
		authorizationURL.Query().Get("client_secret") != "" {
		t.Fatalf("authorization query=%v", authorizationURL.Query())
	}

	identity, err := client.Exchange(context.Background(), "valid-code")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if identity.Subject != "google-subject" || identity.Email != "user@example.com" ||
		identity.Name != "Test User" || !strings.HasPrefix(identity.AvatarURL, "https://lh3.googleusercontent.com/") {
		t.Fatalf("identity=%+v", identity)
	}
	if tokenCalls != 1 || userInfoCalls != 1 {
		t.Fatalf("calls token=%d userinfo=%d", tokenCalls, userInfoCalls)
	}
}

func TestClientRejectsUnverifiedEmailAndUntrustedAvatar(t *testing.T) {
	verified := false
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/token" {
			fmt.Fprint(response, `{"access_token":"token"}`)
			return
		}
		fmt.Fprintf(response, `{"sub":"sub","email":"user@example.com","email_verified":%v,"name":"User","picture":"https://evil.example/avatar"}`, verified)
	}))
	defer server.Close()
	client := newTestClient(t, server)
	if _, err := client.Exchange(context.Background(), "code"); err == nil || !strings.Contains(err.Error(), "подтверждённый email") {
		t.Fatalf("unverified error=%v", err)
	}
	verified = true
	identity, err := client.Exchange(context.Background(), "code")
	if err != nil {
		t.Fatalf("verified exchange: %v", err)
	}
	if identity.AvatarURL != "" {
		t.Fatalf("untrusted avatar=%q", identity.AvatarURL)
	}
}

func TestClientLimitsProviderResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(response, strings.Repeat("x", maxResponseBytes+1))
	}))
	defer server.Close()
	client := newTestClient(t, server)
	if _, err := client.Exchange(context.Background(), "code"); err == nil || !strings.Contains(err.Error(), "превышает") {
		t.Fatalf("oversized error=%v", err)
	}
}

func TestNewRejectsInsecureRemoteEndpoint(t *testing.T) {
	_, err := New(Config{
		ClientID: "id", ClientSecret: "secret", RedirectURL: "https://example.com/callback",
		Endpoints: Endpoints{
			AuthorizationURL: "http://example.com/auth",
			TokenURL:         "https://example.com/token", UserInfoURL: "https://example.com/userinfo",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("error=%v", err)
	}
}

func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := New(Config{
		ClientID: "id", ClientSecret: "secret",
		RedirectURL: "http://localhost:8080/auth/google/callback",
		Endpoints: Endpoints{
			AuthorizationURL: server.URL + "/auth",
			TokenURL:         server.URL + "/token", UserInfoURL: server.URL + "/userinfo",
		},
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}
