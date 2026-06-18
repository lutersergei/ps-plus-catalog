package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"time"

	"ps-extra/internal/psstore"
	"ps-extra/internal/store"
)

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	dbPath := fs.String("db", "ps-extra.db", "путь к файлу SQLite")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	games, err := psstore.FetchCatalog(ctx, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	fmt.Printf("получено игр из каталога: %d\n", len(games))

	for _, g := range games {
		row := store.GameRow{
			ID:          g.ID,
			Title:       g.Title,
			TitleEn:     g.TitleEn,
			ReleaseYear: g.ReleaseYear,
			Genres:      g.Genres,
			Platforms:   g.Platforms,
			ImageURL:    g.ImageURL,
			StoreURL:    g.StoreURL,
		}
		if err := store.UpsertGame(db, row); err != nil {
			return fmt.Errorf("upsert %s: %w", g.ID, err)
		}
		if err := store.SetGenres(db, g.ID, g.Genres); err != nil {
			return fmt.Errorf("set genres %s: %w", g.ID, err)
		}
	}
	fmt.Printf("каталог записан в %s\n", *dbPath)
	return nil
}
