package main

import (
	"fmt"
	"os"

	"github.com/lutersergei/ps-plus-catalog/internal/envfile"
)

func main() {
	// Подхватываем .env (если есть): токены RapidAPI и пр. Реальные переменные
	// окружения имеют приоритет над файлом.
	envPath := ".env"
	if p := os.Getenv("PS_EXTRA_ENV_FILE"); p != "" {
		envPath = p
	}
	if err := envfile.Load(envPath); err != nil {
		fmt.Fprintln(os.Stderr, "env load error:", err)
	}

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
