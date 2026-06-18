package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"ps-extra/internal/psstore"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ps-extra <sync|serve> [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "sync":
		if err := runSync(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "sync error:", err)
			os.Exit(1)
		}
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "serve error:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", os.Args[1])
		os.Exit(2)
	}
}

func runSync(args []string) error {
	ctx := context.Background()
	games, err := psstore.FetchCatalog(ctx, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	fmt.Printf("fetched %d games\n", len(games))
	for i, g := range games {
		if i >= 3 {
			break
		}
		fmt.Printf("  %s | en=%q | year=%d | genres=%v | platforms=%v\n",
			g.Title, g.TitleEn, g.ReleaseYear, g.Genres, g.Platforms)
	}
	return nil
}

func runServe(args []string) error {
	fmt.Println("serve: not implemented yet")
	return nil
}
