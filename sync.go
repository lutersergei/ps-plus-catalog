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
	refreshDays := fs.Int("refresh-days", 30, "не перезапрашивать оценки свежее N дней")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	client := &http.Client{Timeout: 30 * time.Second}

	if err := syncCatalog(ctx, db, client); err != nil {
		return err
	}
	if *skipScores {
		return nil
	}
	return syncScores(ctx, db, client, *maxOC, *refreshDays)
}

// syncCatalog тянет каталог PS Plus и пишет игры + жанры в БД.
func syncCatalog(ctx context.Context, db *sql.DB, client *http.Client) error {
	games, err := psstore.FetchCatalog(ctx, client)
	if err != nil {
		return err
	}
	fmt.Printf("получено игр из каталога: %d\n", len(games))
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
		var mc sql.NullInt64
		if score, found, err := scores.MetacriticScore(ctx, client, t.TitleEn); err != nil {
			log.Printf("[mc] %s: %v", t.Title, err)
		} else if found {
			mc = sql.NullInt64{Int64: int64(score), Valid: true}
		}
		if err := store.UpdateMetacritic(db, t.ID, mc); err != nil {
			return fmt.Errorf("update mc %s: %w", t.ID, err)
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
		var oc sql.NullInt64
		if score, found, err := scores.OpenCriticScore(ctx, client, apiKey, t.TitleEn); err != nil {
			log.Printf("[oc] %s: %v", t.Title, err)
		} else if found {
			oc = sql.NullInt64{Int64: int64(score), Valid: true}
		}
		if err := store.UpdateOpenCritic(db, t.ID, oc); err != nil {
			return fmt.Errorf("update oc %s: %w", t.ID, err)
		}
		fmt.Printf("  OpenCritic %d/%d: %s\n", i+1, len(ocTargets), t.Title)
		time.Sleep(300 * time.Millisecond) // ≤4 req/s
	}
	return nil
}
