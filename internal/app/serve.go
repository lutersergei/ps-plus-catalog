package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/adapters/googleauth"
	"github.com/lutersergei/ps-plus-catalog/internal/adapters/sqlite"
	"github.com/lutersergei/ps-plus-catalog/internal/handlers"
	"github.com/lutersergei/ps-plus-catalog/internal/services"
)

func runServe(
	args []string,
	assets Assets,
	config runtimeConfig,
	logger *slog.Logger,
	stderr io.Writer,
) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "ps-extra.db", "путь к файлу SQLite")
	addr := flags.String("addr", "127.0.0.1:8080", "адрес HTTP-сервера")
	if err := parseFlags(flags, args); err != nil {
		return err
	}

	ctx, stop := commandContext()
	defer stop()
	db, err := sqlite.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	repository := sqlite.NewRepository(db)
	browser := services.NewCatalogService(repository)
	handler, err := newCatalogHandler(assets.IndexHTML, browser, repository, config, logger)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("HTTP-сервер запущен", "url", displayURL(*addr), "db", *dbPath)
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		stop()
		logger.Info("получен сигнал завершения, HTTP-сервер останавливается")
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("остановить HTTP-сервер: %w", err)
		}
		if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func newCatalogHandler(
	templateSource string,
	browser *services.CatalogService,
	repository *sqlite.Repository,
	config runtimeConfig,
	logger *slog.Logger,
) (*handlers.CatalogHandler, error) {
	credentialsConfigured := config.googleClientID != "" || config.googleClientSecret != ""
	if !credentialsConfigured {
		return handlers.NewCatalogHandler(templateSource, browser, logger)
	}
	if config.googleClientID == "" || config.googleClientSecret == "" {
		return nil, errors.New("GOOGLE_CLIENT_ID и GOOGLE_CLIENT_SECRET должны быть заданы вместе")
	}
	if config.publicURL == "" {
		return nil, errors.New("для Google OAuth требуется PS_EXTRA_PUBLIC_URL")
	}
	webURL, err := parseWebURL(config.publicURL)
	if err != nil {
		return nil, err
	}
	oauthClient, err := googleauth.New(googleauth.Config{
		ClientID: config.googleClientID, ClientSecret: config.googleClientSecret,
		RedirectURL: webURL.redirectURL,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	})
	if err != nil {
		return nil, err
	}
	accounts := services.NewAuthService(repository, services.AuthOptions{})
	return handlers.NewCatalogHandlerWithAuth(templateSource, browser, handlers.AuthConfig{
		Accounts: accounts, Google: oauthClient,
		BasePath: webURL.basePath, SecureCookies: webURL.secure,
	}, logger)
}

func displayURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}
