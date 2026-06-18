package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"math"
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

// syncScores собирает Metacritic (всем) и OpenCritic (в пределах maxOC) для игр
// без свежих оценок. Ошибки провайдеров логируются, цикл не прерывается.
func syncScores(ctx context.Context, db *sql.DB, client *http.Client, maxOC, refreshDays int) error {
	apiKey := os.Getenv("OPENCRITIC_API_KEY")
	if apiKey == "" {
		log.Println("OPENCRITIC_API_KEY не задан — OpenCritic пропускается, только Metacritic")
	}

	staleBefore := time.Now().AddDate(0, 0, -refreshDays)
	targets, err := store.GamesNeedingScores(db, staleBefore)
	if err != nil {
		return err
	}

	// OpenCritic — бутылочное горлышко (лимит плана). При наличии ключа обрабатываем
	// за запуск не больше maxOC игр целиком; следующий запуск возьмёт остальные.
	useOC := apiKey != ""
	if useOC && len(targets) > maxOC {
		targets = targets[:maxOC]
	}
	fmt.Printf("игр к обработке за этот запуск: %d\n", len(targets))

	ocUsed := 0
	for i, t := range targets {
		var mc, oc sql.NullInt64

		if score, found, err := scores.MetacriticScore(ctx, client, t.TitleEn); err != nil {
			log.Printf("[mc] %s: %v", t.Title, err)
		} else if found {
			mc = sql.NullInt64{Int64: int64(score), Valid: true}
		}

		if useOC {
			ocUsed++
			if score, found, err := scores.OpenCriticScore(ctx, client, apiKey, t.TitleEn); err != nil {
				log.Printf("[oc] %s: %v", t.Title, err)
			} else if found {
				oc = sql.NullInt64{Int64: int64(score), Valid: true}
			}
		}

		if err := store.UpdateScores(db, t.ID, mc, oc, computeAverage(mc, oc)); err != nil {
			return fmt.Errorf("update scores %s: %w", t.ID, err)
		}

		if (i+1)%10 == 0 {
			fmt.Printf("  обработано %d/%d (OpenCritic использовано %d)\n", i+1, len(targets), ocUsed)
		}
		time.Sleep(700 * time.Millisecond) // вежливо к Metacritic, ≤4 req/s к OpenCritic
	}
	fmt.Printf("готово: обработано %d игр, OpenCritic-запросов: %d\n", len(targets), ocUsed)
	return nil
}

// computeAverage считает среднее из доступных оценок (обе 0–100); NULL, если нет ни одной.
func computeAverage(mc, oc sql.NullInt64) sql.NullFloat64 {
	var sum float64
	var n int
	if mc.Valid {
		sum += float64(mc.Int64)
		n++
	}
	if oc.Valid {
		sum += float64(oc.Int64)
		n++
	}
	if n == 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: math.Round(sum/float64(n)*10) / 10, Valid: true}
}
