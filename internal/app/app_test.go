package app

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestParseAPIKeysTrimsAndDeduplicates(t *testing.T) {
	got := parseAPIKeys(" first,second; first\nthird ", "second")
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("keys=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("keys=%v, want %v", got, want)
		}
	}
}

func TestRunReturnsUsageCodeForUnknownCommand(t *testing.T) {
	t.Setenv("PS_EXTRA_ENV_FILE", t.TempDir()+"/missing.env")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"unknown"}, Assets{}, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestDisplayURLAddsLocalhostForPortOnly(t *testing.T) {
	if got := displayURL(":8080"); got != "http://localhost:8080" {
		t.Fatalf("displayURL=%q", got)
	}
}

func TestParseWebURLBuildsOAuthCallbackBehindPrefix(t *testing.T) {
	config, err := parseWebURL("https://slyuter.store/games/")
	if err != nil {
		t.Fatalf("parse web URL: %v", err)
	}
	if config.basePath != "/games" || !config.secure ||
		config.redirectURL != "https://slyuter.store/games/auth/google/callback" {
		t.Fatalf("config=%+v", config)
	}

	local, err := parseWebURL("http://localhost:8080")
	if err != nil {
		t.Fatalf("parse local URL: %v", err)
	}
	if local.basePath != "" || local.secure ||
		local.redirectURL != "http://localhost:8080/auth/google/callback" {
		t.Fatalf("local config=%+v", local)
	}
}

func TestParseWebURLRejectsInsecureProductionAndAmbiguousParts(t *testing.T) {
	for _, raw := range []string{
		"http://slyuter.store/games",
		"https://user@slyuter.store/games",
		"https://slyuter.store/games?next=evil",
		"https://slyuter.store/games/../admin",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := parseWebURL(raw); err == nil {
				t.Fatalf("parseWebURL(%q) должен вернуть ошибку", raw)
			}
		})
	}
}

func TestReadConfigLoadsGoogleOAuthSettings(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", " client-id ")
	t.Setenv("GOOGLE_CLIENT_SECRET", " client-secret ")
	t.Setenv("PS_EXTRA_PUBLIC_URL", " https://example.com/games ")
	config := readConfig()
	if config.googleClientID != "client-id" || config.googleClientSecret != "client-secret" ||
		config.publicURL != "https://example.com/games" {
		t.Fatalf("config=%+v", config)
	}
	if strings.Contains(strings.TrimSpace(config.publicURL), "client-secret") {
		t.Fatal("public URL не должен содержать client secret")
	}
}

func TestNewCatalogHandlerRequiresCompleteGoogleConfiguration(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for name, config := range map[string]runtimeConfig{
		"missing secret":     {googleClientID: "id"},
		"missing public URL": {googleClientID: "id", googleClientSecret: "secret"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newCatalogHandler("ok", nil, nil, config, logger); err == nil {
				t.Fatal("неполная Google OAuth конфигурация должна вернуть ошибку")
			}
		})
	}
	if _, err := newCatalogHandler("ok", nil, nil, runtimeConfig{}, logger); err != nil {
		t.Fatalf("каталог без опциональной авторизации: %v", err)
	}
}
