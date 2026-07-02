package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/psstore"
	"github.com/lutersergei/ps-plus-catalog/internal/scores"
	"github.com/lutersergei/ps-plus-catalog/internal/store"
)

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	dbPath := fs.String("db", "ps-extra.db", "путь к файлу SQLite")
	skipScores := fs.Bool("skip-scores", false, "только обновить каталог, без оценок")
	allowShrink := fs.Bool("allow-shrink", false, "разрешить применить снимок каталога, даже если он намного меньше текущего (защита от частичного ответа upstream)")
	maxOC := fs.Int("max-oc", 25, "лимит игр OpenCritic на каждый ключ за запуск (суммарно ×кол-во ключей)")
	maxHLTB := fs.Int("max-hltb", 0, "максимум игр для HowLongToBeat за запуск (0 = без ограничения; HLTB троттлит большие пачки)")
	maxLangs := fs.Int("max-langs", 0, "максимум игр для сбора языков (PS Store) за запуск (0 = без ограничения)")
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

	// По SIGINT/SIGTERM отменяем контекст: запросы к источникам и паузы между
	// ними прерываются, активная транзакция откатывается через defer tx.Rollback.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	client := &http.Client{Timeout: 30 * time.Second}

	if err := syncCatalog(ctx, db, client, *allowShrink); err != nil {
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
	if err := syncLangs(ctx, db, client, *refreshDays, *maxLangs); err != nil {
		return err
	}
	return store.RecomputeAllAverages(db)
}

// syncLangs собирает языки озвучки и субтитров для игр со страниц PS Store.
func syncLangs(ctx context.Context, db *sql.DB, client *http.Client, refreshDays, maxLangs int) error {
	staleBefore := time.Now().AddDate(0, 0, -refreshDays)
	targets, err := store.GamesNeedingLangs(db, staleBefore)
	if err != nil {
		return err
	}
	if maxLangs > 0 && len(targets) > maxLangs {
		targets = targets[:maxLangs]
	}
	fmt.Printf("Языки (PS Store) — игр к проверке: %d\n", len(targets))
	for i, t := range targets {
		spoken, screen, err := psstore.FetchLangs(ctx, client, t.ConceptURL)
		if err != nil {
			log.Printf("[langs] %s: %v (повторим позже)", t.ID, err)
		} else {
			if err := store.UpdateLangs(db, t.ID, spoken, screen); err != nil {
				return fmt.Errorf("update langs %s: %w", t.ID, err)
			}
		}
		if (i+1)%10 == 0 {
			fmt.Printf("  Языки %d/%d\n", i+1, len(targets))
		}
		if err := sleepCtx(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}

// sleepCtx ждёт d или отмену контекста — паузы между запросами к источникам
// должны прерываться по SIGINT/SIGTERM, а не висеть до конца таймаута.
func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
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
		res, found, conclusive, err := session.Lookup(ctx, t.TitleEn)
		switch {
		case err != nil:
			// сбой/блок — НЕ помечаем проверенным, повторим в следующий запуск
			log.Printf("[hltb] %s: %v (повторим позже)", t.Title, err)
		case !found && !conclusive:
			// пустая выдача по всем вариантам — вероятен троттлинг. Не помечаем
			// проверенной, чтобы промах подобрался в следующий запуск.
			log.Printf("[hltb] %s: пустая выдача (вероятно троттл, повторим позже)", t.Title)
		case !found:
			// HLTB вернул игры, но нужной среди них нет — достоверно «нет на HLTB».
			// Кэшируем промах (NULL-значения + отметка проверки), чтобы не дёргать
			// HLTB по этой игре каждый запуск (см. -refresh-days).
			if err := store.UpdateHLTB(db, t.ID, sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{}, sql.NullString{}); err != nil {
				return fmt.Errorf("update hltb %s: %w", t.ID, err)
			}
		default:
			var mainExtra, rating, hltbID sql.NullInt64
			var hltbURL sql.NullString
			if res.MainExtraSeconds > 0 {
				mainExtra = sql.NullInt64{Int64: int64(res.MainExtraSeconds), Valid: true}
			}
			if res.Rating > 0 {
				rating = sql.NullInt64{Int64: int64(res.Rating), Valid: true}
			}
			if res.GameID > 0 {
				hltbID = sql.NullInt64{Int64: int64(res.GameID), Valid: true}
			}
			if res.PageURL != "" {
				hltbURL = sql.NullString{String: res.PageURL, Valid: true}
			}
			if err := store.UpdateHLTB(db, t.ID, mainExtra, rating, hltbID, hltbURL); err != nil {
				return fmt.Errorf("update hltb %s: %w", t.ID, err)
			}
		}
		if (i+1)%25 == 0 {
			fmt.Printf("  HowLongToBeat %d/%d\n", i+1, len(targets))
		}
		if err := sleepCtx(ctx, 1300*time.Millisecond); err != nil { // HLTB троттлит частые запросы — держим паузу побольше
			return err
		}
	}
	return nil
}

// catalogShrinkLimit — порог защиты: если новый снимок меньше текущего активного
// каталога более чем на эту долю, sync прерывается (вероятен частичный ответ
// upstream). Снять защиту можно флагом -allow-shrink.
const catalogShrinkLimit = 0.30

// syncCatalog тянет каталог PS Plus и пишет игры + жанры в БД одной транзакцией.
// Игры, не вошедшие в текущий снимок, помечаются active=0.
func syncCatalog(ctx context.Context, db *sql.DB, client *http.Client, allowShrink bool) error {
	games, err := psstore.FetchCatalog(ctx, client)
	if err != nil {
		return err
	}
	fmt.Printf("получено игр из каталога: %d\n", len(games))

	// Защита от аномального сжатия: если каталог резко уменьшился, вероятен
	// частичный/битый ответ upstream — массовая деактивация была бы ошибкой.
	prevActive, err := store.CountActive(db)
	if err != nil {
		return err
	}
	if !allowShrink && prevActive > 0 && len(games) < int(float64(prevActive)*(1-catalogShrinkLimit)) {
		return fmt.Errorf(
			"снимок каталога подозрительно мал: было активных %d, в ответе %d (падение > %.0f%%); "+
				"если это ожидаемо — повторите с -allow-shrink",
			prevActive, len(games), catalogShrinkLimit*100)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	ids := make([]string, 0, len(games))
	for _, g := range games {
		row := store.GameRow{
			ID: g.ID, Title: g.Title, TitleEn: g.TitleEn,
			ReleaseYear: g.ReleaseYear, Genres: g.Genres,
			Platforms: g.Platforms, ImageURL: g.ImageURL, StoreURL: g.StoreURL,
		}
		if err := store.UpsertGame(tx, row); err != nil {
			return fmt.Errorf("upsert %s: %w", g.ID, err)
		}
		if err := store.SetGenres(tx, g.ID, g.Genres); err != nil {
			return fmt.Errorf("set genres %s: %w", g.ID, err)
		}
		ids = append(ids, g.ID)
	}

	deactivated, err := store.DeactivateMissing(tx, ids)
	if err != nil {
		return fmt.Errorf("deactivate missing: %w", err)
	}
	if deactivated > 0 {
		fmt.Printf("деактивировано %d игр, покинувших PS Plus Extra\n", deactivated)
	}

	if err := tx.Commit(); err != nil {
		return err
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
		res, err := scores.MetacriticScores(ctx, client, t.TitleEn)
		if err != nil {
			// сетевой сбой/блок/5xx — НЕ помечаем проверенным, повторим в следующий запуск
			log.Printf("[mc] %s: %v (повторим позже)", t.Title, err)
		} else {
			// успех: либо нашли оценку, либо достоверно «нет» (found=false → NULL)
			var mc sql.NullInt64
			if res.Critic.Found {
				mc = sql.NullInt64{Int64: int64(res.Critic.Score), Valid: true}
			}
			var userScore sql.NullInt64
			var userCount sql.NullInt64
			if res.User.Found {
				userScore = sql.NullInt64{Int64: int64(res.User.Score), Valid: true}
				userCount = sql.NullInt64{Int64: int64(res.User.Count), Valid: true}
			}
			if res.UserErr != nil {
				log.Printf("[mc-user] %s: %v", t.Title, res.UserErr)
			}
			var mcURL sql.NullString
			if res.PageURL != "" {
				mcURL = sql.NullString{String: res.PageURL, Valid: true}
			}
			if err := store.UpdateMetacriticScores(db, t.ID, mc, userScore, userCount, mcURL); err != nil {
				return fmt.Errorf("update mc %s: %w", t.ID, err)
			}
		}
		if (i+1)%25 == 0 {
			fmt.Printf("  Metacritic %d/%d\n", i+1, len(mcTargets))
		}
		if err := sleepCtx(ctx, 700*time.Millisecond); err != nil { // вежливо к metacritic.com
			return err
		}
	}

	// --- OpenCritic: один или несколько ключей RapidAPI с ротацией при 429 ---
	pool := scores.NewKeyPool(openCriticKeys())
	if pool.Empty() {
		fmt.Println("ключи OpenCritic не заданы — OpenCritic пропущен (см. .env / OPENCRITIC_API_KEYS)")
		return nil
	}
	// maxOC трактуется как лимит на КАЖДЫЙ ключ → суммарно maxOC*кол-во ключей.
	effMax := maxOC * pool.Count()
	ocTargets, err := store.GamesNeedingOpenCritic(db, staleBefore)
	if err != nil {
		return err
	}
	if effMax > 0 && len(ocTargets) > effMax {
		ocTargets = ocTargets[:effMax]
	}
	siteKey := openCriticSiteKey()
	fmt.Printf("OpenCritic — ключей: %d, игр за этот запуск: %d\n", pool.Count(), len(ocTargets))
	for i, t := range ocTargets {
		res, err := scores.OpenCriticScores(ctx, client, pool, siteKey, t.TitleEn)
		if errors.Is(err, scores.ErrAllKeysExhausted) {
			fmt.Println("  все ключи OpenCritic исчерпали дневную квоту — остановка (добёрём в следующий запуск)")
			break
		}
		if err != nil {
			// сбой/5xx — НЕ помечаем проверенным: повторим позже
			log.Printf("[oc] %s: %v (повторим позже)", t.Title, err)
		} else {
			var oc sql.NullInt64
			if res.Critic.Found {
				oc = sql.NullInt64{Int64: int64(res.Critic.Score), Valid: true}
			}
			var ocURL sql.NullString
			if res.PageURL != "" {
				ocURL = sql.NullString{String: res.PageURL, Valid: true}
			}
			var ocID sql.NullInt64
			if res.ID > 0 {
				ocID = sql.NullInt64{Int64: int64(res.ID), Valid: true}
			}
			var playerScore sql.NullInt64
			var playerCount sql.NullInt64
			if res.Player.Found {
				playerScore = sql.NullInt64{Int64: int64(res.Player.Score), Valid: true}
				playerCount = sql.NullInt64{Int64: int64(res.Player.Count), Valid: true}
			}
			if res.PlayerErr != nil {
				log.Printf("[oc-player] %s: %v", t.Title, res.PlayerErr)
			}
			if err := store.UpdateOpenCriticScores(db, t.ID, oc, ocURL, ocID, playerScore, playerCount); err != nil {
				return fmt.Errorf("update oc %s: %w", t.ID, err)
			}
		}
		fmt.Printf("  OpenCritic %d/%d: %s\n", i+1, len(ocTargets), t.Title)
		if err := sleepCtx(ctx, 300*time.Millisecond); err != nil { // ≤4 req/s
			return err
		}
	}
	return nil
}

// openCriticKeys собирает ключи RapidAPI из окружения: OPENCRITIC_API_KEYS
// (через запятую/пробел/перенос строки) плюс одиночный OPENCRITIC_API_KEY.
func openCriticKeys() []string {
	seen := map[string]bool{}
	var keys []string
	add := func(k string) {
		k = strings.TrimSpace(k)
		if k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for _, k := range strings.FieldsFunc(os.Getenv("OPENCRITIC_API_KEYS"), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == ';'
	}) {
		add(k)
	}
	add(os.Getenv("OPENCRITIC_API_KEY"))
	return keys
}

func openCriticSiteKey() string {
	return strings.TrimSpace(os.Getenv("OPENCRITIC_SITE_API_KEY"))
}
