// Package app содержит разбор CLI и ручную сборку зависимостей приложения.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/adapters/psstore"
	"github.com/lutersergei/ps-plus-catalog/internal/adapters/sqlite"
	"github.com/lutersergei/ps-plus-catalog/internal/infrastructure/envfile"
	"github.com/lutersergei/ps-plus-catalog/internal/services"
)

// Assets содержит статические данные, встраиваемые корневым пакетом.
type Assets struct {
	IndexHTML           string
	CatalogDateBackfill []byte
}

// Run загружает конфигурацию, выполняет выбранную команду и возвращает код
// завершения процесса.
func Run(args []string, assets Assets, stdout, stderr io.Writer) int {
	envPath := ".env"
	if configured := os.Getenv("PS_EXTRA_ENV_FILE"); configured != "" {
		envPath = configured
	}
	if err := envfile.Load(envPath); err != nil {
		fmt.Fprintln(stderr, "env load error:", err)
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: ps-extra <sync|sync-dates|serve> [flags]")
		return 2
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	config := readConfig()
	var err error
	switch args[0] {
	case "sync":
		err = runSync(args[1:], assets, config, logger, stdout, stderr)
	case "sync-dates":
		err = runSyncDates(args[1:], assets, logger, stdout, stderr)
	case "serve":
		err = runServe(args[1:], assets, logger, stderr)
	default:
		fmt.Fprintln(stderr, "unknown command:", args[0])
		return 2
	}
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	var usageErr *flagError
	if errors.As(err, &usageErr) {
		return 2
	}
	fmt.Fprintf(stderr, "%s error: %v\n", args[0], err)
	return 1
}

type runtimeConfig struct {
	openCriticKeys    []string
	openCriticSiteKey string
}

func readConfig() runtimeConfig {
	return runtimeConfig{
		openCriticKeys:    parseAPIKeys(os.Getenv("OPENCRITIC_API_KEYS"), os.Getenv("OPENCRITIC_API_KEY")),
		openCriticSiteKey: strings.TrimSpace(os.Getenv("OPENCRITIC_SITE_API_KEY")),
	}
}

func parseAPIKeys(list, single string) []string {
	seen := make(map[string]bool)
	var keys []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		keys = append(keys, value)
	}
	for _, key := range strings.FieldsFunc(list, func(char rune) bool {
		return char == ',' || char == ' ' || char == '\n' || char == '\t' || char == ';'
	}) {
		add(key)
	}
	add(single)
	return keys
}

type flagError struct{ err error }

func (e *flagError) Error() string { return e.err.Error() }
func (e *flagError) Unwrap() error { return e.err }

func parseFlags(set *flag.FlagSet, args []string) error {
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return flag.ErrHelp
		}
		return &flagError{err: err}
	}
	return nil
}

func commandContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

func dateService(
	repository *sqlite.Repository,
	playStation *psstore.Client,
	assets Assets,
	logger *slog.Logger,
) (*services.CatalogDateService, error) {
	return services.NewCatalogDateService(services.CatalogDateDependencies{
		Repository:    repository,
		Source:        playStation,
		BackfillJSON:  assets.CatalogDateBackfill,
		ParserVersion: psstore.CatalogAnnouncementParserVersion,
		Logger:        logger,
	})
}
