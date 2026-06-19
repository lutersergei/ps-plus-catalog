package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"ps-extra/internal/psstore"
	"ps-extra/internal/scores"
	"ps-extra/internal/store"
)

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	dbPath := fs.String("db", "ps-extra.db", "путь к файлу SQLite")
	skipScores := fs.Bool("skip-scores", false, "только обновить каталог, без оценок")
	maxOC := fs.Int("max-oc", 25, "максимум обращений к OpenCritic за запуск (лимит плана)")
	maxHLTB := fs.Int("max-hltb", 0, "максимум игр для HowLongToBeat за запуск (0 = без ограничения; HLTB троттлит большие пачки)")
	refreshDays := fs.Int("refresh-days", 30, "не перезапрашивать оценки свежее N дней")
	recheckMissing := fs.Bool("recheck-missing", false, "сбросить отметки проверки у игр без оценки и перепроверить их")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	if *recheckMissing {
		mc, oc, err := store.ResetMissingChecks(db)
		if err != nil {
			return err
		}
		fmt.Printf("сброшены отметки проверки: Metacritic %d, OpenCritic %d игр — будут перепроверены\n", mc, oc)
	}

	ctx := context.Background()
	client := &http.Client{Timeout: 30 * time.Second}

	if err := syncCatalog(ctx, db, client); err != nil {
		return err
	}
	if *skipScores {
		return nil
	}
	if err := syncScores(ctx, db, client, *maxOC, *refreshDays); err != nil {
		return err
	}
	if err := syncHLTB(ctx, db, client, *refreshDays, *maxHLTB); err != nil {
		return err
	}
	return store.RecomputeAllAverages(db)
}

// syncHLTB собирает время Main+Sides и рейтинг с HowLongToBeat для всех игр без
// свежей проверки (источник бесплатный, без дневного лимита).
func syncHLTB(ctx context.Context, db *sql.DB, client *http.Client, refreshDays, maxHLTB int) error {
	staleBefore := time.Now().AddDate(0, 0, -refreshDays)
	targets, err := store.GamesNeedingHLTB(db, staleBefore)
	if err != nil {
		return err
	}
	if maxHLTB > 0 && len(targets) > maxHLTB {
		targets = targets[:maxHLTB]
	}
	fmt.Printf("HowLongToBeat — игр к проверке: %d\n", len(targets))
	session := scores.NewHLTBSession(client)
	for i, t := range targets {
		res, found, err := session.Lookup(ctx, t.TitleEn)
		switch {
		case err != nil:
			// сбой/блок — НЕ помечаем проверенным, повторим в следующий запуск
			log.Printf("[hltb] %s: %v (повторим позже)", t.Title, err)
		case !found:
			// пустой ответ: троттл или игры нет на HLTB. Не помечаем проверенной,
			// чтобы троттл-промахи подобрались в следующий запуск.
			log.Printf("[hltb] %s: совпадений нет (повторим позже)", t.Title)
		default:
			var mainExtra, rating sql.NullInt64
			if res.MainExtraSeconds > 0 {
				mainExtra = sql.NullInt64{Int64: int64(res.MainExtraSeconds), Valid: true}
			}
			if res.Rating > 0 {
				rating = sql.NullInt64{Int64: int64(res.Rating), Valid: true}
			}
			if err := store.UpdateHLTB(db, t.ID, mainExtra, rating); err != nil {
				return fmt.Errorf("update hltb %s: %w", t.ID, err)
			}
		}
		if (i+1)%25 == 0 {
			fmt.Printf("  HowLongToBeat %d/%d\n", i+1, len(targets))
		}
		time.Sleep(1300 * time.Millisecond) // HLTB троттлит частые запросы (пустой ответ) — держим паузу побольше
	}
	return nil
}

// syncCatalog тянет каталог PS Plus и пишет игры + жанры в БД.
func syncCatalog(ctx context.Context, db *sql.DB, client *http.Client) error {
	games, err := psstore.FetchCatalog(ctx, client)
	if err != nil {
		return err
	}
	uniq := make(map[string]bool, len(games))
	for _, g := range games {
		uniq[g.ID] = true
	}
	fmt.Printf("получено записей из каталога: %d (уникальных игр: %d, дубли изданий схлопнуты по conceptId)\n",
		len(games), len(uniq))
	for _, g := range games {
		row := store.GameRow{
			ID: g.ID, Title: g.Title, TitleEn: g.TitleEn,
			ReleaseYear: g.ReleaseYear, Genres: g.Genres,
			Platforms: g.Platforms, ImageURL: g.ImageURL, StoreURL: g.StoreURL,
		}
		if err := store.UpsertGame(db, row); err != nil {
			return fmt.Errorf("upsert %s: %w", g.ID, err)
		}
		if err := store.SetGenres(db, g.ID, g.Genres); err != nil {
			return fmt.Errorf("set genres %s: %w", g.ID, err)
		}
	}
	fmt.Println("каталог записан")
	return nil
}

// syncScores собирает Metacritic для ВСЕХ игр без свежей проверки (источник
// бесплатный) и OpenCritic — порциями не больше maxOC за запуск (лимит плана).
// Источники независимы: отсутствие ключа не мешает собрать все Metacritic.
// Ошибки провайдеров логируются, цикл не прерывается.
func syncScores(ctx context.Context, db *sql.DB, client *http.Client, maxOC, refreshDays int) error {
	staleBefore := time.Now().AddDate(0, 0, -refreshDays)

	// --- Metacritic: все нуждающиеся игры ---
	mcTargets, err := store.GamesNeedingMetacritic(db, staleBefore)
	if err != nil {
		return err
	}
	fmt.Printf("Metacritic — игр к проверке: %d\n", len(mcTargets))
	for i, t := range mcTargets {
		score, found, err := scores.MetacriticScore(ctx, client, t.TitleEn)
		if err != nil {
			// сетевой сбой/блок/5xx — НЕ помечаем проверенным, повторим в следующий запуск
			log.Printf("[mc] %s: %v (повторим позже)", t.Title, err)
		} else {
			// успех: либо нашли оценку, либо достоверно «нет» (found=false → NULL)
			var mc sql.NullInt64
			if found {
				mc = sql.NullInt64{Int64: int64(score), Valid: true}
			}
			if err := store.UpdateMetacritic(db, t.ID, mc); err != nil {
				return fmt.Errorf("update mc %s: %w", t.ID, err)
			}
		}
		if (i+1)%25 == 0 {
			fmt.Printf("  Metacritic %d/%d\n", i+1, len(mcTargets))
		}
		time.Sleep(700 * time.Millisecond) // вежливо к metacritic.com
	}

	// --- OpenCritic: только при наличии ключа, не больше maxOC за запуск ---
	apiKey := os.Getenv("OPENCRITIC_API_KEY")
	if apiKey == "" {
		fmt.Println("OPENCRITIC_API_KEY не задан — OpenCritic пропущен (запустите с ключом, чтобы добрать)")
		return nil
	}
	ocTargets, err := store.GamesNeedingOpenCritic(db, staleBefore)
	if err != nil {
		return err
	}
	if len(ocTargets) > maxOC {
		ocTargets = ocTargets[:maxOC]
	}
	fmt.Printf("OpenCritic — игр за этот запуск: %d (осталось добрать в следующие дни)\n", len(ocTargets))
	for i, t := range ocTargets {
		score, found, err := scores.OpenCriticScore(ctx, client, apiKey, t.TitleEn)
		if err != nil {
			// сбой/429/5xx — НЕ помечаем проверенным и НЕ тратим попытку: повторим позже
			log.Printf("[oc] %s: %v (повторим позже)", t.Title, err)
		} else {
			var oc sql.NullInt64
			if found {
				oc = sql.NullInt64{Int64: int64(score), Valid: true}
			}
			if err := store.UpdateOpenCritic(db, t.ID, oc); err != nil {
				return fmt.Errorf("update oc %s: %w", t.ID, err)
			}
		}
		fmt.Printf("  OpenCritic %d/%d: %s\n", i+1, len(ocTargets), t.Title)
		time.Sleep(300 * time.Millisecond) // ≤4 req/s
	}
	return nil
}
