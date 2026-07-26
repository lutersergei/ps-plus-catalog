package app

import (
	"flag"
	"fmt"
	"io"
	"log/slog"

	"github.com/lutersergei/ps-plus-catalog/internal/adapters/psstore"
	"github.com/lutersergei/ps-plus-catalog/internal/adapters/scores"
	"github.com/lutersergei/ps-plus-catalog/internal/adapters/sqlite"
	"github.com/lutersergei/ps-plus-catalog/internal/services"
)

func runSync(
	args []string,
	assets Assets,
	config runtimeConfig,
	logger *slog.Logger,
	stdout, stderr io.Writer,
) error {
	flags := flag.NewFlagSet("sync", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "ps-extra.db", "путь к файлу SQLite")
	skipScores := flags.Bool("skip-scores", false, "только обновить каталог, без оценок")
	allowShrink := flags.Bool("allow-shrink", false, "разрешить применить аномально маленький снимок каталога")
	maxOpenCritic := flags.Int("max-oc", 25, "лимит игр OpenCritic на каждый ключ за запуск")
	maxHLTB := flags.Int("max-hltb", 0, "максимум игр HowLongToBeat за запуск (0 = без ограничения)")
	maxLanguages := flags.Int("max-langs", 0, "максимум игр для сбора языков (0 = без ограничения)")
	refreshDays := flags.Int("refresh-days", 30, "не перезапрашивать оценки свежее N дней")
	recheckMissing := flags.Bool("recheck-missing", false, "перепроверить игры без оценки")
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
	client := httpClient()
	playStation := psstore.NewClient(client)
	dates, err := dateService(repository, playStation, assets, logger)
	if err != nil {
		return fmt.Errorf("подготовить историю дат: %w", err)
	}
	service := services.NewSyncService(services.SyncDependencies{
		Repository:  repository,
		PlayStation: playStation,
		Metacritic:  scores.NewMetacriticClient(client),
		OpenCritic:  scores.NewOpenCriticClient(client, config.openCriticKeys, config.openCriticSiteKey),
		HLTB:        scores.NewHLTBClient(client),
		Dates:       dates,
		Logger:      logger,
	})
	report, err := service.Run(ctx, services.SyncOptions{
		SkipScores:     *skipScores,
		AllowShrink:    *allowShrink,
		MaxOpenCritic:  *maxOpenCritic,
		MaxHLTB:        *maxHLTB,
		MaxLanguages:   *maxLanguages,
		RefreshDays:    *refreshDays,
		RecheckMissing: *recheckMissing,
	})
	if err != nil {
		return err
	}
	printSyncReport(stdout, report, len(config.openCriticKeys) > 0, *skipScores)
	return nil
}

func printSyncReport(output io.Writer, report services.SyncReport, openCriticEnabled, skipScores bool) {
	if report.Reset.Metacritic > 0 || report.Reset.OpenCritic > 0 {
		fmt.Fprintf(output, "сброшены отметки проверки: Metacritic %d, OpenCritic %d игр — будут перепроверены\n", report.Reset.Metacritic, report.Reset.OpenCritic)
	}
	fmt.Fprintf(output, "получено игр из каталога: %d\n", report.CatalogGames)
	switch {
	case report.Catalog.Initial:
		fmt.Fprintf(output, "история каталога инициализирована: %d игр, дата добавления пока неизвестна\n", report.Catalog.Added)
	case report.Catalog.Added > 0:
		fmt.Fprintf(output, "обнаружено новых или вернувшихся игр: %d\n", report.Catalog.Added)
	}
	if report.Catalog.Deactivated > 0 {
		fmt.Fprintf(output, "деактивировано %d игр, покинувших PS Plus Extra\n", report.Catalog.Deactivated)
	}
	fmt.Fprintln(output, "каталог записан")
	if report.DatesErr == nil {
		printCatalogDateStats(output, report.Dates, false)
	}
	if skipScores {
		return
	}
	fmt.Fprintf(output, "Metacritic — обработано игр: %d\n", report.Metacritic)
	if !openCriticEnabled {
		fmt.Fprintln(output, "ключи OpenCritic не заданы — OpenCritic пропущен (см. .env / OPENCRITIC_API_KEYS)")
	} else {
		fmt.Fprintf(output, "OpenCritic — обработано игр: %d\n", report.OpenCritic)
	}
	if report.QuotaExhausted {
		fmt.Fprintln(output, "все ключи OpenCritic исчерпали дневную квоту — продолжим в следующий запуск")
	}
	fmt.Fprintf(output, "HowLongToBeat — обработано игр: %d\n", report.HLTB)
	fmt.Fprintf(output, "Языки (PS Store) — обработано игр: %d\n", report.Languages)
}
