package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/psstore"
	"github.com/lutersergei/ps-plus-catalog/internal/store"
)

type catalogDateSyncStats struct {
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

type catalogDateCacheStats struct {
	Downloaded  int
	Cached      int
	ParseErrors int
}

type catalogDateMatchResult struct {
	Matches            []store.CatalogDateMatch
	ResetMembershipIDs []int64
	Matched            int
	AmbiguousGames     []string
	UnmatchedGames     []string
}

func runSyncDates(args []string) error {
	fs := flag.NewFlagSet("sync-dates", flag.ExitOnError)
	dbPath := fs.String("db", "ps-extra.db", "путь к файлу SQLite")
	refresh := fs.Bool("refresh", false, "повторно скачать все анонсы, игнорируя sitemap-кэш")
	verbose := fs.Bool("verbose", false, "показать игры без однозначного совпадения")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	client := &http.Client{Timeout: 30 * time.Second}

	stats, err := syncCatalogDates(ctx, db, client, *refresh, time.Now().UTC().Year())
	if err != nil {
		return err
	}
	printCatalogDateStats(stats, *verbose)
	return nil
}

func syncCatalogDates(
	ctx context.Context,
	db *sql.DB,
	client *http.Client,
	refresh bool,
	currentYear int,
) (catalogDateSyncStats, error) {
	var stats catalogDateSyncStats
	backfill, err := loadCatalogDateBackfill()
	if err != nil {
		return stats, err
	}
	refs, err := psstore.FetchAnnouncementIndex(ctx, client, currentYear)
	if err != nil {
		return stats, err
	}
	stats.Announcements = len(refs)

	cacheStats, err := refreshCatalogAnnouncementCache(ctx, db, client, refs, refresh)
	if err != nil {
		return stats, err
	}
	stats.Downloaded = cacheStats.Downloaded
	stats.Cached = cacheStats.Cached
	stats.ParseErrors = cacheStats.ParseErrors

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return stats, err
	}
	defer tx.Rollback()
	if err := store.AcquireCatalogSyncLock(tx); err != nil {
		return stats, err
	}

	storedCandidates, err := store.CatalogDateCandidates(tx)
	if err != nil {
		return stats, err
	}
	stats.Candidates = len(storedCandidates)
	targets, err := store.CurrentCatalogDateTargets(tx)
	if err != nil {
		return stats, err
	}
	stats.Targets = len(targets)
	if len(targets) == 0 {
		return stats, fmt.Errorf("в БД нет текущих периодов каталога: сначала выполните sync")
	}

	backfillResult, err := matchCatalogDateBackfillTargets(targets, backfill)
	if err != nil {
		return stats, err
	}
	matchResult := matchCatalogDateTargets(backfillResult.AnnouncementTargets, storedCandidates)
	stats.Verified = len(backfillResult.Matches)
	stats.KeptNull = len(backfillResult.KeepNullIDs)
	stats.Matched = matchResult.Matched + stats.Verified
	stats.AmbiguousGames = matchResult.AmbiguousGames
	stats.UnmatchedGames = append(matchResult.UnmatchedGames, backfillResult.KeepNullGames...)
	stats.Ambiguous = len(matchResult.AmbiguousGames)
	stats.Unmatched = len(stats.UnmatchedGames)
	backfillUpdated, err := store.ApplyCatalogDateBackfillTx(
		tx,
		backfillResult.Matches,
		backfillResult.KeepNullIDs,
	)
	if err != nil {
		return stats, err
	}
	announcementUpdated, err := store.ApplyCatalogDateChangesTx(
		tx,
		matchResult.Matches,
		matchResult.ResetMembershipIDs,
	)
	if err != nil {
		return stats, err
	}
	stats.Updated = backfillUpdated + announcementUpdated
	if err := tx.Commit(); err != nil {
		return stats, err
	}
	return stats, nil
}

// refreshCatalogAnnouncementCache обновляет только устаревшие разобранные
// статьи. При ошибке прежний кэш намеренно остаётся нетронутым: его можно
// использовать дальше, а следующая синхронизация повторит попытку.
func refreshCatalogAnnouncementCache(
	ctx context.Context,
	db *sql.DB,
	client *http.Client,
	refs []psstore.AnnouncementRef,
	refresh bool,
) (catalogDateCacheStats, error) {
	var stats catalogDateCacheStats
	versions, err := store.AnnouncementVersions(db)
	if err != nil {
		return stats, err
	}
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		if !refresh {
			if version, ok := versions[ref.URL]; ok &&
				version.LastModified == ref.LastModified &&
				version.ParserVersion == psstore.CatalogAnnouncementParserVersion {
				stats.Cached++
				continue
			}
		}
		announcement, err := psstore.FetchCatalogAnnouncement(ctx, client, ref)
		if err != nil {
			if ctx.Err() != nil {
				return stats, ctx.Err()
			}
			stats.ParseErrors++
			log.Printf("[dates] анонс %s пропущен: %v", ref.URL, err)
			continue
		}
		row := store.AnnouncementRow{
			URL:           announcement.URL,
			LastModified:  announcement.LastModified,
			ParserVersion: psstore.CatalogAnnouncementParserVersion,
			PublishedOn:   announcement.PublishedOn,
			Games:         make([]store.AnnouncementGameRow, 0, len(announcement.Games)),
		}
		for _, game := range announcement.Games {
			row.Games = append(row.Games, store.AnnouncementGameRow{Title: game.Title, AddedOn: game.AddedOn})
		}
		if err := store.ReplaceAnnouncement(db, row); err != nil {
			return stats, fmt.Errorf("cache announcement %s: %w", ref.URL, err)
		}
		stats.Downloaded++
		if err := sleepCtx(ctx, 100*time.Millisecond); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

// catalogDateMatchWindowDays — максимальное расхождение между анонсом и первым
// наблюдением для периодов после запуска истории. У первого снимка first_seen —
// дата запуска сервиса, а не добавления в PS Plus, поэтому он использует весь
// архив без такого окна.
const catalogDateMatchWindowDays = 45

// utcDate обрезает время до календарного UTC-дня. Окно матчинга ведётся по
// дням: AddedOn из SQLite DATE — 00:00 UTC, FirstSeenAt — полный timestamp.
func utcDate(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// matchCatalogDateTargets сопоставляет названия с анонсами без I/O. Для первого
// снимка first_seen игнорируется. У последующих периодов нижняя граница — дата
// закрытия предыдущего периода, а при её отсутствии — first_seen минус окно.
// Это не даёт повторно использовать анонс прошлого появления после того, как
// source уже сменился с observed на announcement.
func matchCatalogDateTargets(
	targets []store.CatalogDateTarget,
	candidates []store.CatalogDateCandidate,
) catalogDateMatchResult {
	result := catalogDateMatchResult{Matches: make([]store.CatalogDateMatch, 0, len(targets))}
	for _, target := range targets {
		eligible := make([]psstore.CatalogAddition, 0, len(candidates))
		var minDate, maxDate time.Time
		windowed := !target.Initial
		if windowed {
			seenDay := utcDate(target.FirstSeenAt)
			minDate = seenDay.AddDate(0, 0, -catalogDateMatchWindowDays)
			maxDate = seenDay.AddDate(0, 0, catalogDateMatchWindowDays)
			if target.PreviousRemovedOn.Valid {
				previousRemovedDay := utcDate(target.PreviousRemovedOn.Time)
				if previousRemovedDay.After(minDate) {
					minDate = previousRemovedDay
				}
			}
			if target.AddedOn.Valid {
				currentAddedDay := utcDate(target.AddedOn.Time)
				if currentAddedDay.Before(minDate) || currentAddedDay.After(maxDate) {
					result.ResetMembershipIDs = append(result.ResetMembershipIDs, target.MembershipID)
				}
			}
		}
		for _, candidate := range candidates {
			if windowed {
				addedDay := utcDate(candidate.AddedOn)
				if addedDay.Before(minDate) || addedDay.After(maxDate) {
					continue
				}
			}
			eligible = append(eligible, psstore.CatalogAddition{
				Title: candidate.Title, AddedOn: candidate.AddedOn, SourceURL: candidate.SourceURL,
			})
		}
		match, found, ambiguous := psstore.MatchCatalogAddition(target.Title, target.TitleEn, eligible)
		switch {
		case ambiguous:
			result.AmbiguousGames = append(result.AmbiguousGames, target.Title)
		case !found:
			result.UnmatchedGames = append(result.UnmatchedGames, target.Title)
		default:
			result.Matched++
			result.Matches = append(result.Matches, store.CatalogDateMatch{
				MembershipID: target.MembershipID,
				AddedOn:      match.AddedOn,
				SourceURL:    match.SourceURL,
			})
		}
	}
	return result
}

func printCatalogDateStats(stats catalogDateSyncStats, verbose bool) {
	fmt.Printf(
		"Даты каталога — анонсов: %d (скачано %d, из кэша %d, ошибок разбора %d), записей: %d\n",
		stats.Announcements,
		stats.Downloaded,
		stats.Cached,
		stats.ParseErrors,
		stats.Candidates,
	)
	fmt.Printf(
		"Даты каталога — игр: %d, совпало %d (проверено вручную %d), неоднозначно %d, без даты %d (оставлено намеренно %d), обновлено %d\n",
		stats.Targets,
		stats.Matched,
		stats.Verified,
		stats.Ambiguous,
		stats.Unmatched,
		stats.KeptNull,
		stats.Updated,
	)
	if verbose && len(stats.AmbiguousGames) > 0 {
		fmt.Println("Неоднозначные совпадения:")
		for _, title := range stats.AmbiguousGames {
			fmt.Printf("  - %s\n", title)
		}
	}
	if verbose && len(stats.UnmatchedGames) > 0 {
		fmt.Println("Без подтверждённой даты:")
		for _, title := range stats.UnmatchedGames {
			fmt.Printf("  - %s\n", title)
		}
	}
}
