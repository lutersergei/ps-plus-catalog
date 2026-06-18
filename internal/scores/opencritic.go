package scores

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"time"
)

const ocHost = "opencritic-api.p.rapidapi.com"

// ocSearchResult — элемент ответа /game/search.
type ocSearchResult struct {
	ID   int     `json:"id"`
	Name string  `json:"name"`
	Dist float64 `json:"dist"`
}

// OpenCriticScore ищет игру по названию и возвращает её Top Critic Score.
// found=false, если совпадений нет. apiKey — ключ RapidAPI.
func OpenCriticScore(ctx context.Context, c *http.Client, apiKey, title string) (score int, found bool, err error) {
	results, err := ocSearch(ctx, c, apiKey, title)
	if err != nil {
		return 0, false, err
	}
	best, ok := bestMatch(title, results)
	if !ok {
		return 0, false, nil
	}
	raw, err := ocGet(ctx, c, apiKey, fmt.Sprintf("/game/%d", best.ID))
	if err != nil {
		return 0, false, err
	}
	s, err := parseOpenCriticGame(raw)
	if err != nil {
		return 0, false, err
	}
	return s, true, nil
}

// bestMatch выбирает результат поиска: точное совпадение нормализованного
// названия, иначе ближайший по dist.
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
	return results[0], true
}

// parseOpenCriticGame достаёт topCriticScore из ответа /game/<id>.
func parseOpenCriticGame(raw []byte) (int, error) {
	var g struct {
		TopCriticScore float64 `json:"topCriticScore"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return 0, fmt.Errorf("parse opencritic game: %w", err)
	}
	if g.TopCriticScore < 0 {
		return 0, nil
	}
	return int(math.Round(g.TopCriticScore)), nil
}

func ocSearch(ctx context.Context, c *http.Client, apiKey, title string) ([]ocSearchResult, error) {
	raw, err := ocGet(ctx, c, apiKey, "/game/search?criteria="+url.QueryEscape(title))
	if err != nil {
		return nil, err
	}
	var results []ocSearchResult
	if err := json.Unmarshal(raw, &results); err != nil {
		return nil, fmt.Errorf("parse opencritic search: %w", err)
	}
	return results, nil
}

// ocGet выполняет GET к OpenCritic с повтором при 429/5xx.
func ocGet(ctx context.Context, c *http.Client, apiKey, path string) ([]byte, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+ocHost+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-RapidAPI-Key", apiKey)
		req.Header.Set("X-RapidAPI-Host", ocHost)

		resp, err := c.Do(req)
		if err != nil {
			lastErr = err
		} else {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusOK:
				if readErr != nil {
					return nil, readErr
				}
				return body, nil
			case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
				lastErr = fmt.Errorf("opencritic status %d", resp.StatusCode)
			default:
				return nil, fmt.Errorf("opencritic status %d: %s", resp.StatusCode, string(body))
			}
		}
		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
	}
	return nil, lastErr
}
