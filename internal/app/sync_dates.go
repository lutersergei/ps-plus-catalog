package app

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/adapters/psstore"
	"github.com/lutersergei/ps-plus-catalog/internal/adapters/sqlite"
	"github.com/lutersergei/ps-plus-catalog/internal/services"
)

func runSyncDates(args []string, assets Assets, logger *slog.Logger, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("sync-dates", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "ps-extra.db", "путь к файлу SQLite")
	refresh := flags.Bool("refresh", false, "повторно скачать все анонсы")
	verbose := flags.Bool("verbose", false, "показать игры без однозначного совпадения")
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
	service, err := dateService(repository, psstore.NewClient(httpClient()), assets, logger)
	if err != nil {
		return fmt.Errorf("подготовить историю дат: %w", err)
	}
	stats, err := service.Sync(ctx, *refresh, time.Now().UTC().Year())
	if err != nil {
		return err
	}
	printCatalogDateStats(stdout, stats, *verbose)
	return nil
}

func printCatalogDateStats(output io.Writer, stats services.CatalogDateSyncStats, verbose bool) {
	fmt.Fprintf(output,
		"Даты каталога — анонсов: %d (скачано %d, из кэша %d, ошибок разбора %d), записей: %d\n",
		stats.Announcements, stats.Downloaded, stats.Cached, stats.ParseErrors, stats.Candidates,
	)
	fmt.Fprintf(output,
		"Даты каталога — игр: %d, совпало %d (проверено вручную %d), неоднозначно %d, без даты %d (оставлено намеренно %d), обновлено %d\n",
		stats.Targets, stats.Matched, stats.Verified, stats.Ambiguous, stats.Unmatched, stats.KeptNull, stats.Updated,
	)
	if verbose && len(stats.AmbiguousGames) > 0 {
		fmt.Fprintln(output, "Неоднозначные совпадения:")
		for _, title := range stats.AmbiguousGames {
			fmt.Fprintf(output, "  - %s\n", title)
		}
	}
	if verbose && len(stats.UnmatchedGames) > 0 {
		fmt.Fprintln(output, "Без подтверждённой даты:")
		for _, title := range stats.UnmatchedGames {
			fmt.Fprintf(output, "  - %s\n", title)
		}
	}
}
