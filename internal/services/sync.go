package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

const catalogShrinkLimit = 0.30

// SyncRepository описывает операции хранения, необходимые полной синхронизации.
type SyncRepository interface {
	CountActive(context.Context) (int, error)
	ApplyCatalogSnapshot(context.Context, []domain.CatalogGame, time.Time) (domain.CatalogSnapshotResult, error)
	GamesNeedingMetacritic(context.Context, time.Time) ([]domain.ScoreTarget, error)
	GamesNeedingOpenCritic(context.Context, time.Time) ([]domain.ScoreTarget, error)
	GamesNeedingHLTB(context.Context, time.Time) ([]domain.ScoreTarget, error)
	GamesNeedingLanguages(context.Context, time.Time) ([]domain.LanguageTarget, error)
	UpdateMetacritic(context.Context, string, domain.MetacriticUpdate) error
	UpdateMetacriticPageURL(context.Context, string, *string) error
	UpdateOpenCritic(context.Context, string, domain.OpenCriticUpdate) error
	UpdateHLTB(context.Context, string, domain.HLTBUpdate) error
	UpdateLanguages(context.Context, string, []string, []string) error
	SetSourceGenres(context.Context, string, string, []domain.SourceGenre) error
	ResetMissingChecks(context.Context) (domain.ResetMissingResult, error)
	RecomputeAllAverages(context.Context) error
}

// PlayStationSource предоставляет каталог и языковые метаданные PS Store.
type PlayStationSource interface {
	FetchCatalog(context.Context) ([]domain.CatalogGame, error)
	FetchLanguages(context.Context, string) ([]string, []string, error)
}

// MetacriticSource предоставляет оценки и жанры Metacritic.
type MetacriticSource interface {
	Lookup(context.Context, string) (domain.MetacriticResult, error)
}

// OpenCriticSource предоставляет оценки OpenCritic и сведения о доступных ключах.
type OpenCriticSource interface {
	Enabled() bool
	KeyCount() int
	Lookup(context.Context, string) (domain.OpenCriticResult, error)
}

// HLTBSource предоставляет длительность и пользовательский рейтинг игры.
type HLTBSource interface {
	Lookup(context.Context, string) (domain.HLTBResult, bool, bool, error)
}

// CatalogDateSynchronizer обновляет даты появления игр независимо от оценок.
type CatalogDateSynchronizer interface {
	Sync(context.Context, bool, int) (CatalogDateSyncStats, error)
}

// SyncOptions задаёт политику одного запуска полной синхронизации.
type SyncOptions struct {
	SkipScores     bool
	AllowShrink    bool
	MaxOpenCritic  int
	MaxHLTB        int
	MaxLanguages   int
	RefreshDays    int
	RecheckMissing bool
}

// SyncReport содержит наблюдаемый итог полной синхронизации.
type SyncReport struct {
	Catalog        domain.CatalogSnapshotResult
	CatalogGames   int
	Reset          domain.ResetMissingResult
	Dates          CatalogDateSyncStats
	DatesErr       error
	Metacritic     int
	OpenCritic     int
	HLTB           int
	Languages      int
	QuotaExhausted bool
}

// SyncDependencies группирует явные зависимости сервиса синхронизации.
type SyncDependencies struct {
	Repository  SyncRepository
	PlayStation PlayStationSource
	Metacritic  MetacriticSource
	OpenCritic  OpenCriticSource
	HLTB        HLTBSource
	Dates       CatalogDateSynchronizer
	Logger      *slog.Logger
	Now         func() time.Time
	Sleep       func(context.Context, time.Duration) error
}

// SyncService выполняет полный сценарий обновления каталога.
type SyncService struct {
	repository  SyncRepository
	playStation PlayStationSource
	metacritic  MetacriticSource
	openCritic  OpenCriticSource
	hltb        HLTBSource
	dates       CatalogDateSynchronizer
	logger      *slog.Logger
	now         func() time.Time
	sleep       func(context.Context, time.Duration) error
}

// NewSyncService создаёт сервис и подставляет безопасные системные зависимости
// для часов, ожидания и журналирования, если они не переданы явно.
func NewSyncService(deps SyncDependencies) *SyncService {
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	sleep := deps.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	return &SyncService{
		repository:  deps.Repository,
		playStation: deps.PlayStation,
		metacritic:  deps.Metacritic,
		openCritic:  deps.OpenCritic,
		hltb:        deps.HLTB,
		dates:       deps.Dates,
		logger:      logger,
		now:         now,
		sleep:       sleep,
	}
}

// Run выполняет синхронизацию в прежнем порядке: каталог, даты, оценки, HLTB
// и языки. Ошибка дат остаётся некритичной, как и до разделения слоёв.
func (s *SyncService) Run(ctx context.Context, options SyncOptions) (SyncReport, error) {
	var report SyncReport
	if options.RecheckMissing {
		reset, err := s.repository.ResetMissingChecks(ctx)
		if err != nil {
			return report, fmt.Errorf("сбросить отметки проверки: %w", err)
		}
		report.Reset = reset
	}
	games, err := s.playStation.FetchCatalog(ctx)
	if err != nil {
		return report, fmt.Errorf("получить каталог PlayStation: %w", err)
	}
	report.CatalogGames = len(games)
	active, err := s.repository.CountActive(ctx)
	if err != nil {
		return report, fmt.Errorf("посчитать активные игры: %w", err)
	}
	if !options.AllowShrink && active > 0 && len(games) < int(float64(active)*(1-catalogShrinkLimit)) {
		return report, fmt.Errorf(
			"снимок каталога подозрительно мал: было активных %d, в ответе %d (падение > %.0f%%); если это ожидаемо — повторите с -allow-shrink",
			active,
			len(games),
			catalogShrinkLimit*100,
		)
	}
	report.Catalog, err = s.repository.ApplyCatalogSnapshot(ctx, games, s.now().UTC())
	if err != nil {
		return report, fmt.Errorf("применить снимок каталога: %w", err)
	}

	if s.dates != nil {
		report.Dates, report.DatesErr = s.dates.Sync(ctx, false, s.now().UTC().Year())
		if report.DatesErr != nil {
			s.logger.Warn("даты из анонсов не обновлены", "error", report.DatesErr)
		}
	}
	if options.SkipScores {
		return report, nil
	}
	staleBefore := s.now().AddDate(0, 0, -options.RefreshDays)
	if report.Metacritic, err = s.syncMetacritic(ctx, staleBefore); err != nil {
		return report, err
	}
	if report.OpenCritic, report.QuotaExhausted, err = s.syncOpenCritic(ctx, staleBefore, options.MaxOpenCritic); err != nil {
		return report, err
	}
	if report.HLTB, err = s.syncHLTB(ctx, staleBefore, options.MaxHLTB); err != nil {
		return report, err
	}
	if report.Languages, err = s.syncLanguages(ctx, staleBefore, options.MaxLanguages); err != nil {
		return report, err
	}
	if err := s.repository.RecomputeAllAverages(ctx); err != nil {
		return report, fmt.Errorf("пересчитать средние оценки: %w", err)
	}
	return report, nil
}

func (s *SyncService) syncMetacritic(ctx context.Context, staleBefore time.Time) (int, error) {
	targets, err := s.repository.GamesNeedingMetacritic(ctx, staleBefore)
	if err != nil {
		return 0, fmt.Errorf("получить цели Metacritic: %w", err)
	}
	for i, target := range targets {
		result, lookupErr := s.metacritic.Lookup(ctx, target.TitleEn)
		switch {
		case lookupErr != nil:
			s.logger.Warn("Metacritic временно недоступен", "game", target.Title, "error", lookupErr)
		case target.NeedsMetacriticURLBackfill:
			if result.Critic.Found && result.PageURL != "" {
				if err := s.repository.UpdateMetacriticPageURL(ctx, target.ID, stringPointer(result.PageURL)); err != nil {
					return i, fmt.Errorf("обновить URL Metacritic для %s: %w", target.ID, err)
				}
			} else {
				s.logger.Warn("страница для дозаполнения URL Metacritic не найдена", "game", target.Title)
			}
		default:
			if result.UserErr != nil {
				s.logger.Warn("пользовательская оценка Metacritic недоступна", "game", target.Title, "error", result.UserErr)
			}
			update := domain.MetacriticUpdate{
				Critic:    ratingScore(result.Critic),
				User:      ratingScore(result.User),
				UserCount: ratingCount(result.User),
				PageURL:   stringPointer(result.PageURL),
			}
			if err := s.repository.UpdateMetacritic(ctx, target.ID, update); err != nil {
				return i, fmt.Errorf("обновить Metacritic для %s: %w", target.ID, err)
			}
			if err := s.repository.SetSourceGenres(ctx, target.ID, "metacritic", sourceGenres(result.Genres)); err != nil {
				return i, fmt.Errorf("обновить жанры Metacritic для %s: %w", target.ID, err)
			}
		}
		if err := s.sleep(ctx, 700*time.Millisecond); err != nil {
			return i + 1, err
		}
	}
	return len(targets), nil
}

func (s *SyncService) syncOpenCritic(ctx context.Context, staleBefore time.Time, perKeyLimit int) (int, bool, error) {
	if s.openCritic == nil || !s.openCritic.Enabled() {
		return 0, false, nil
	}
	targets, err := s.repository.GamesNeedingOpenCritic(ctx, staleBefore)
	if err != nil {
		return 0, false, fmt.Errorf("получить цели OpenCritic: %w", err)
	}
	limit := providerRunLimit(perKeyLimit, s.openCritic.KeyCount())
	if limit > 0 && len(targets) > limit {
		targets = targets[:limit]
	}
	processed := 0
	for _, target := range targets {
		result, lookupErr := s.openCritic.Lookup(ctx, target.TitleEn)
		if errors.Is(lookupErr, domain.ErrProviderQuotaExhausted) {
			return processed, true, nil
		}
		if lookupErr != nil {
			s.logger.Warn("OpenCritic временно недоступен", "game", target.Title, "error", lookupErr)
		} else {
			if result.PlayerErr != nil {
				s.logger.Warn("пользовательская оценка OpenCritic недоступна", "game", target.Title, "error", result.PlayerErr)
			}
			update := domain.OpenCriticUpdate{
				Critic:      ratingScore(result.Critic),
				PageURL:     stringPointer(result.PageURL),
				ID:          positiveIntPointer(result.ID),
				Player:      ratingScore(result.Player),
				PlayerCount: ratingCount(result.Player),
			}
			if err := s.repository.UpdateOpenCritic(ctx, target.ID, update); err != nil {
				return processed, false, fmt.Errorf("обновить OpenCritic для %s: %w", target.ID, err)
			}
			if err := s.repository.SetSourceGenres(ctx, target.ID, "opencritic", openCriticGenres(result.Genres)); err != nil {
				return processed, false, fmt.Errorf("обновить жанры OpenCritic для %s: %w", target.ID, err)
			}
		}
		processed++
		if err := s.sleep(ctx, 300*time.Millisecond); err != nil {
			return processed, false, err
		}
	}
	return processed, false, nil
}

func (s *SyncService) syncHLTB(ctx context.Context, staleBefore time.Time, maximum int) (int, error) {
	targets, err := s.repository.GamesNeedingHLTB(ctx, staleBefore)
	if err != nil {
		return 0, fmt.Errorf("получить цели HLTB: %w", err)
	}
	if maximum > 0 && len(targets) > maximum {
		targets = targets[:maximum]
	}
	for i, target := range targets {
		result, found, conclusive, lookupErr := s.hltb.Lookup(ctx, target.TitleEn)
		switch {
		case lookupErr != nil:
			s.logger.Warn("HLTB временно недоступен", "game", target.Title, "error", lookupErr)
		case !found && !conclusive:
			s.logger.Warn("HLTB вернул пустую выдачу, проверка будет повторена", "game", target.Title)
		case !found:
			if err := s.repository.UpdateHLTB(ctx, target.ID, domain.HLTBUpdate{}); err != nil {
				return i, fmt.Errorf("обновить HLTB для %s: %w", target.ID, err)
			}
		default:
			update := domain.HLTBUpdate{
				MainExtraSeconds: positiveIntPointer(result.MainExtraSeconds),
				Rating:           positiveIntPointer(result.Rating),
				ID:               positiveIntPointer(result.GameID),
				PageURL:          stringPointer(result.PageURL),
			}
			if err := s.repository.UpdateHLTB(ctx, target.ID, update); err != nil {
				return i, fmt.Errorf("обновить HLTB для %s: %w", target.ID, err)
			}
		}
		if err := s.sleep(ctx, 1300*time.Millisecond); err != nil {
			return i + 1, err
		}
	}
	return len(targets), nil
}

func (s *SyncService) syncLanguages(ctx context.Context, staleBefore time.Time, maximum int) (int, error) {
	targets, err := s.repository.GamesNeedingLanguages(ctx, staleBefore)
	if err != nil {
		return 0, fmt.Errorf("получить цели языков: %w", err)
	}
	if maximum > 0 && len(targets) > maximum {
		targets = targets[:maximum]
	}
	for i, target := range targets {
		spoken, screen, lookupErr := s.playStation.FetchLanguages(ctx, target.ConceptURL)
		if lookupErr != nil {
			s.logger.Warn("языки PS Store временно недоступны", "game_id", target.ID, "error", lookupErr)
		} else if err := s.repository.UpdateLanguages(ctx, target.ID, spoken, screen); err != nil {
			return i, fmt.Errorf("обновить языки для %s: %w", target.ID, err)
		}
		if err := s.sleep(ctx, 500*time.Millisecond); err != nil {
			return i + 1, err
		}
	}
	return len(targets), nil
}

func providerRunLimit(perKey, keyCount int) int {
	if perKey <= 0 || keyCount <= 0 {
		return 0
	}
	if perKey > math.MaxInt/keyCount {
		return math.MaxInt
	}
	return perKey * keyCount
}

func ratingScore(rating domain.Rating) *int64 {
	if !rating.Found {
		return nil
	}
	value := int64(rating.Score)
	return &value
}

func ratingCount(rating domain.Rating) *int64 {
	if !rating.Found {
		return nil
	}
	value := int64(rating.Count)
	return &value
}

func positiveIntPointer(value int) *int64 {
	if value <= 0 {
		return nil
	}
	result := int64(value)
	return &result
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func sourceGenres(genres []string) []domain.SourceGenre {
	result := make([]domain.SourceGenre, 0, len(genres))
	for _, genre := range genres {
		result = append(result, domain.SourceGenre{Name: genre})
	}
	return result
}

func openCriticGenres(genres []domain.OpenCriticGenre) []domain.SourceGenre {
	result := make([]domain.SourceGenre, 0, len(genres))
	for _, genre := range genres {
		var sourceID *int64
		if genre.ID > 0 {
			value := int64(genre.ID)
			sourceID = &value
		}
		result = append(result, domain.SourceGenre{Name: genre.Name, SourceID: sourceID})
	}
	return result
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
