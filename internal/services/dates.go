package services

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

const catalogDateMatchWindowDays = 45

// AnnouncementRepository описывает кэш анонсов и атомарное уточнение дат.
type AnnouncementRepository interface {
	AnnouncementVersions(context.Context) (map[string]domain.AnnouncementCacheVersion, error)
	ReplaceAnnouncement(context.Context, domain.CachedAnnouncement) error
	ReconcileCatalogDates(
		context.Context,
		func([]domain.CatalogDateTarget, []domain.CatalogDateCandidate) (domain.CatalogDatePlan, error),
	) (domain.CatalogDateApplyResult, error)
}

// AnnouncementSource предоставляет индекс и содержимое официальных анонсов.
type AnnouncementSource interface {
	FetchAnnouncementIndex(context.Context, int) ([]domain.AnnouncementRef, error)
	FetchAnnouncement(context.Context, domain.AnnouncementRef) (domain.CatalogAnnouncement, error)
}

// CatalogDateSyncStats содержит итог обновления дат появления игр.
type CatalogDateSyncStats struct {
	Announcements  int
	Downloaded     int
	Cached         int
	ParseErrors    int
	Candidates     int
	Targets        int
	Matched        int
	Verified       int
	KeptNull       int
	Ambiguous      int
	Unmatched      int
	Updated        int64
	AmbiguousGames []string
	UnmatchedGames []string
}

// CatalogDateDependencies группирует зависимости сервиса дат каталога.
type CatalogDateDependencies struct {
	Repository    AnnouncementRepository
	Source        AnnouncementSource
	BackfillJSON  []byte
	ParserVersion int
	Logger        *slog.Logger
	Sleep         func(context.Context, time.Duration) error
}

// CatalogDateService обновляет кэш официальных анонсов и сопоставляет их играм.
type CatalogDateService struct {
	repository    AnnouncementRepository
	source        AnnouncementSource
	backfill      catalogDateBackfill
	parserVersion int
	logger        *slog.Logger
	sleep         func(context.Context, time.Duration) error
}

// NewCatalogDateService проверяет встроенный исторический манифест и создаёт
// сервис. Ошибка манифеста обнаруживается при запуске, до изменения базы.
func NewCatalogDateService(deps CatalogDateDependencies) (*CatalogDateService, error) {
	backfill, err := loadCatalogDateBackfill(deps.BackfillJSON)
	if err != nil {
		return nil, err
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	sleep := deps.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	return &CatalogDateService{
		repository:    deps.Repository,
		source:        deps.Source,
		backfill:      backfill,
		parserVersion: deps.ParserVersion,
		logger:        logger,
		sleep:         sleep,
	}, nil
}

// Sync загружает изменившиеся анонсы и одной транзакцией уточняет даты текущих
// периодов каталога.
func (s *CatalogDateService) Sync(
	ctx context.Context,
	refresh bool,
	currentYear int,
) (CatalogDateSyncStats, error) {
	var stats CatalogDateSyncStats
	refs, err := s.source.FetchAnnouncementIndex(ctx, currentYear)
	if err != nil {
		return stats, fmt.Errorf("получить индекс анонсов: %w", err)
	}
	stats.Announcements = len(refs)
	if err := s.refreshCache(ctx, refs, refresh, &stats); err != nil {
		return stats, err
	}

	applyResult, err := s.repository.ReconcileCatalogDates(ctx, func(
		targets []domain.CatalogDateTarget,
		candidates []domain.CatalogDateCandidate,
	) (domain.CatalogDatePlan, error) {
		if len(targets) == 0 {
			return domain.CatalogDatePlan{}, fmt.Errorf("в БД нет текущих периодов каталога: сначала выполните sync")
		}
		backfillResult, err := matchCatalogDateBackfillTargets(targets, s.backfill)
		if err != nil {
			return domain.CatalogDatePlan{}, err
		}
		matchResult := matchCatalogDateTargets(backfillResult.AnnouncementTargets, candidates)
		stats.Verified = len(backfillResult.Matches)
		stats.KeptNull = len(backfillResult.KeepNullIDs)
		stats.Matched = matchResult.Matched + stats.Verified
		stats.AmbiguousGames = matchResult.AmbiguousGames
		stats.UnmatchedGames = append(append([]string(nil), matchResult.UnmatchedGames...), backfillResult.KeepNullGames...)
		stats.Ambiguous = len(stats.AmbiguousGames)
		stats.Unmatched = len(stats.UnmatchedGames)
		return domain.CatalogDatePlan{
			BackfillMatches:     backfillResult.Matches,
			KeepNullIDs:         backfillResult.KeepNullIDs,
			AnnouncementMatches: matchResult.Matches,
			ResetMembershipIDs:  matchResult.ResetMembershipIDs,
		}, nil
	})
	if err != nil {
		return stats, fmt.Errorf("сопоставить даты каталога: %w", err)
	}
	stats.Candidates = applyResult.Candidates
	stats.Targets = applyResult.Targets
	stats.Updated = applyResult.Updated
	return stats, nil
}

func (s *CatalogDateService) refreshCache(
	ctx context.Context,
	refs []domain.AnnouncementRef,
	refresh bool,
	stats *CatalogDateSyncStats,
) error {
	versions, err := s.repository.AnnouncementVersions(ctx)
	if err != nil {
		return fmt.Errorf("прочитать версии анонсов: %w", err)
	}
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !refresh {
			if version, ok := versions[ref.URL]; ok &&
				version.LastModified == ref.LastModified &&
				version.ParserVersion == s.parserVersion {
				stats.Cached++
				continue
			}
		}
		announcement, err := s.source.FetchAnnouncement(ctx, ref)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			stats.ParseErrors++
			s.logger.Warn("анонс пропущен", "url", ref.URL, "error", err)
			continue
		}
		if err := s.repository.ReplaceAnnouncement(ctx, domain.CachedAnnouncement{
			CatalogAnnouncement: announcement,
			ParserVersion:       s.parserVersion,
		}); err != nil {
			return fmt.Errorf("сохранить кэш анонса %s: %w", ref.URL, err)
		}
		stats.Downloaded++
		if err := s.sleep(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}

type catalogDateMatchResult struct {
	Matches            []domain.CatalogDateMatch
	ResetMembershipIDs []int64
	Matched            int
	AmbiguousGames     []string
	UnmatchedGames     []string
}

// matchCatalogDateTargets сопоставляет названия без I/O. Для повторного периода
// используются границы предыдущего удаления и окна первого наблюдения.
func matchCatalogDateTargets(
	targets []domain.CatalogDateTarget,
	candidates []domain.CatalogDateCandidate,
) catalogDateMatchResult {
	result := catalogDateMatchResult{Matches: make([]domain.CatalogDateMatch, 0, len(targets))}
	for _, target := range targets {
		eligible := make([]domain.CatalogAddition, 0, len(candidates))
		var minimum, maximum time.Time
		windowed := !target.Initial
		if windowed {
			seenDay := utcDate(target.FirstSeenAt)
			minimum = seenDay.AddDate(0, 0, -catalogDateMatchWindowDays)
			maximum = seenDay.AddDate(0, 0, catalogDateMatchWindowDays)
			if target.PreviousRemovedOn != nil {
				previousRemovedDay := utcDate(*target.PreviousRemovedOn)
				if previousRemovedDay.After(minimum) {
					minimum = previousRemovedDay
				}
			}
			if target.AddedOn != nil {
				currentAddedDay := utcDate(*target.AddedOn)
				if currentAddedDay.Before(minimum) || currentAddedDay.After(maximum) {
					result.ResetMembershipIDs = append(result.ResetMembershipIDs, target.MembershipID)
				}
			}
		}
		for _, candidate := range candidates {
			if windowed {
				addedDay := utcDate(candidate.AddedOn)
				if addedDay.Before(minimum) || addedDay.After(maximum) {
					continue
				}
			}
			eligible = append(eligible, domain.CatalogAddition(candidate))
		}
		match, found, ambiguous := domain.MatchCatalogAddition(target.Title, target.TitleEn, eligible)
		switch {
		case ambiguous:
			result.AmbiguousGames = append(result.AmbiguousGames, target.Title)
		case !found:
			result.UnmatchedGames = append(result.UnmatchedGames, target.Title)
		default:
			result.Matched++
			result.Matches = append(result.Matches, domain.CatalogDateMatch{
				MembershipID: target.MembershipID,
				AddedOn:      match.AddedOn,
				SourceURL:    match.SourceURL,
			})
		}
	}
	return result
}

func utcDate(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
