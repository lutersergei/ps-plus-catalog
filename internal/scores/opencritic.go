package scores

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"time"
)

const ocHost = "opencritic-api.p.rapidapi.com"

// ErrAllKeysExhausted — все ключи RapidAPI исчерпали дневную квоту (429).
var ErrAllKeysExhausted = errors.New("opencritic: все ключи RapidAPI исчерпали квоту")

// KeyPool — набор ключей RapidAPI с ротацией: при 429 текущий ключ помечается
// исчерпанным и берётся следующий.
type KeyPool struct {
	keys      []string
	exhausted []bool
	cur       int
}

// NewKeyPool создаёт пул из ключей (пустые отбрасываются).
func NewKeyPool(keys []string) *KeyPool {
	var clean []string
	for _, k := range keys {
		if k != "" {
			clean = append(clean, k)
		}
	}
	return &KeyPool{keys: clean, exhausted: make([]bool, len(clean))}
}

// Count — число ключей в пуле.
func (p *KeyPool) Count() int { return len(p.keys) }

// Empty — нет ни одного ключа.
func (p *KeyPool) Empty() bool { return len(p.keys) == 0 }

// current возвращает текущий не исчерпанный ключ.
func (p *KeyPool) current() (string, bool) {
	for i := 0; i < len(p.keys); i++ {
		idx := (p.cur + i) % len(p.keys)
		if !p.exhausted[idx] {
			p.cur = idx
			return p.keys[idx], true
		}
	}
	return "", false
}

// markExhausted помечает текущий ключ исчерпанным и переходит к следующему.
func (p *KeyPool) markExhausted() {
	if len(p.keys) == 0 {
		return
	}
	p.exhausted[p.cur] = true
	p.cur = (p.cur + 1) % len(p.keys)
}

// ocSearchResult — элемент ответа /game/search.
type ocSearchResult struct {
	ID   int     `json:"id"`
	Name string  `json:"name"`
	Dist float64 `json:"dist"`
}

// OpenCriticScore ищет игру по названию и возвращает её Top Critic Score.
// found=false, если совпадений нет. Ключи берутся из пула (с ротацией при 429);
// если все ключи исчерпаны — вернётся ErrAllKeysExhausted.
func OpenCriticScore(ctx context.Context, c *http.Client, pool *KeyPool, title string) (score int, found bool, pageURL string, err error) {
	res, err := OpenCriticScores(ctx, c, pool, "", title)
	if err != nil {
		return 0, false, "", err
	}
	return res.Critic.Score, res.Critic.Found, res.PageURL, nil
}

// OpenCriticResult содержит critic score, canonical URL, OpenCritic id и
// опциональный Player Rating. PlayerErr не фатален для critic score.
type OpenCriticResult struct {
	ID        int
	Critic    Rating
	Player    Rating
	PageURL   string
	PlayerErr error
}

// OpenCriticScores ищет игру и возвращает critic score плюс player rating, если
// задан bearer-токен публичного API сайта.
func OpenCriticScores(ctx context.Context, c *http.Client, pool *KeyPool, siteKey, title string) (OpenCriticResult, error) {
	results, err := ocSearch(ctx, c, pool, CleanTitle(title))
	if err != nil {
		return OpenCriticResult{}, err
	}
	best, ok := bestMatch(title, results)
	if !ok {
		return OpenCriticResult{}, nil
	}
	raw, err := ocGet(ctx, c, pool, fmt.Sprintf("/game/%d", best.ID))
	if err != nil {
		return OpenCriticResult{}, err
	}
	score, found, pageURL, err := parseOpenCriticGame(raw)
	if err != nil {
		return OpenCriticResult{}, err
	}
	res := OpenCriticResult{ID: best.ID, PageURL: pageURL}
	if found {
		res.Critic = Rating{Score: score, Found: true}
	}
	if siteKey != "" {
		player, err := openCriticPlayerRating(ctx, c, siteKey, best.ID)
		if err != nil {
			res.PlayerErr = err
		} else {
			res.Player = player
		}
	}
	return res, nil
}

// ocMaxDist — максимальный search-distance OpenCritic, при котором ближайший
// результат ещё рассматривается как кандидат (эмпирически: точные/почти точные
// совпадения дают dist ≈ 0, явно чужие игры — заметно больше).
const ocMaxDist = 1.0

// bestMatch выбирает результат поиска консервативно: сперва точное совпадение
// нормализованного названия; иначе ближайший по dist — но только если он близок
// (dist ≤ ocMaxDist) И проходит токен-проверку названия. Иначе совпадения нет:
// лучше пропустить игру, чем записать ей оценку другой игры.
func bestMatch(title string, results []ocSearchResult) (ocSearchResult, bool) {
	if len(results) == 0 {
		return ocSearchResult{}, false
	}
	want := NormalizeTitle(title)
	for _, r := range results {
		if NormalizeTitle(r.Name) == want {
			return r, true
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Dist < results[j].Dist })
	for _, r := range results {
		if r.Dist > ocMaxDist {
			break
		}
		if TitlesMatch(title, r.Name) {
			return r, true
		}
	}
	return ocSearchResult{}, false
}

// parseOpenCriticGame достаёт topCriticScore из ответа /game/<id>.
// Поле декодируется в *float64, чтобы отличить отсутствие/null от настоящего
// нуля. found=false, если оценки нет (поле отсутствует, null, NaN/Inf, вне
// диапазона 0–100 или ≤0 — у непрорецензированных игр OpenCritic отдаёт 0/-1).
// Иначе ноль попал бы в БД и занизил average_score.
func parseOpenCriticGame(raw []byte) (score int, found bool, pageURL string, err error) {
	var g struct {
		TopCriticScore *float64 `json:"topCriticScore"`
		URL            string   `json:"url"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return 0, false, "", fmt.Errorf("parse opencritic game: %w", err)
	}
	if g.TopCriticScore == nil {
		return 0, false, g.URL, nil
	}
	v := *g.TopCriticScore
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v > 100 {
		return 0, false, g.URL, nil
	}
	return int(math.Round(v)), true, g.URL, nil
}

func openCriticPlayerRating(ctx context.Context, c *http.Client, siteKey string, gameID int) (Rating, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.opencritic.com/api/ratings/game/%d", gameID), nil)
	if err != nil {
		return Rating{}, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Authorization", "Bearer "+siteKey)
	req.Header.Set("Origin", "https://opencritic.com")
	req.Header.Set("Referer", fmt.Sprintf("https://opencritic.com/game/%d/", gameID))
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36")
	resp, err := c.Do(req)
	if err != nil {
		return Rating{}, fmt.Errorf("opencritic player rating fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Rating{}, nil
	}
	body, readErr := readLimited(resp.Body, maxJSONBytes)
	if readErr != nil {
		return Rating{}, readErr
	}
	if resp.StatusCode != http.StatusOK {
		return Rating{}, fmt.Errorf("opencritic player rating status %d: %s", resp.StatusCode, string(body))
	}
	score, count, found, err := parseOpenCriticPlayerRating(body)
	if err != nil {
		return Rating{}, err
	}
	return Rating{Score: score, Count: count, Found: found}, nil
}

func parseOpenCriticPlayerRating(raw []byte) (score int, count int, found bool, err error) {
	var r struct {
		Median *float64 `json:"median"`
		Count  int      `json:"count"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return 0, 0, false, fmt.Errorf("parse opencritic player rating: %w", err)
	}
	if r.Median == nil {
		return 0, 0, false, nil
	}
	v := *r.Median
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
		return 0, 0, false, nil
	}
	return int(math.Round(v)), r.Count, true, nil
}

func ocSearch(ctx context.Context, c *http.Client, pool *KeyPool, title string) ([]ocSearchResult, error) {
	raw, err := ocGet(ctx, c, pool, "/game/search?criteria="+url.QueryEscape(title))
	if err != nil {
		return nil, err
	}
	var results []ocSearchResult
	if err := json.Unmarshal(raw, &results); err != nil {
		return nil, fmt.Errorf("parse opencritic search: %w", err)
	}
	return results, nil
}

// ocGet выполняет GET к OpenCritic: при 429 ротирует ключ, при 5xx/сетевой
// ошибке повторяет с backoff на текущем ключе.
func ocGet(ctx context.Context, c *http.Client, pool *KeyPool, path string) ([]byte, error) {
	const maxRetries = 3
	for {
		key, ok := pool.current()
		if !ok {
			return nil, ErrAllKeysExhausted
		}
		var lastErr error
		retry := false
		for attempt := 1; attempt <= maxRetries; attempt++ {
			body, status, err := ocDo(ctx, c, key, path)
			switch {
			case err == nil && status == http.StatusOK:
				return body, nil
			case status == http.StatusTooManyRequests:
				pool.markExhausted() // квота этого ключа кончилась — берём следующий
				retry = true
			case status >= 500 || err != nil:
				if err != nil {
					lastErr = err
				} else {
					lastErr = fmt.Errorf("opencritic status %d", status)
				}
			default:
				return nil, fmt.Errorf("opencritic status %d: %s", status, string(body))
			}
			if retry {
				break // выходим во внешний цикл за новым ключом
			}
			if attempt < maxRetries {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(attempt) * time.Second):
				}
			}
		}
		if retry {
			continue
		}
		return nil, lastErr
	}
}

// ocDo делает один запрос и возвращает тело, статус и ошибку транспорта.
func ocDo(ctx context.Context, c *http.Client, key, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+ocHost+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-RapidAPI-Key", key)
	req.Header.Set("X-RapidAPI-Host", ocHost)
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, readErr := readLimited(resp.Body, maxJSONBytes)
	if readErr != nil {
		return nil, resp.StatusCode, readErr
	}
	return body, resp.StatusCode, nil
}
