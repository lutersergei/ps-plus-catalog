package scores

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
)

const mcUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var ldJSONRe = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)

// MetacriticScore возвращает Metascore игры по её английскому названию.
// found=false, если страница недоступна, игра не найдена или нет рецензий.
func MetacriticScore(ctx context.Context, c *http.Client, titleEn string) (score int, found bool, err error) {
	url := "https://www.metacritic.com/game/" + Slugify(titleEn) + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("User-Agent", mcUserAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("metacritic fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return 0, false, nil // игры нет под таким slug
	}
	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("metacritic status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, false, err
	}
	return parseMetacritic(body)
}

// parseMetacritic извлекает Metascore из JSON-LD страницы.
func parseMetacritic(html []byte) (int, bool, error) {
	for _, m := range ldJSONRe.FindAllSubmatch(html, -1) {
		var obj struct {
			AggregateRating struct {
				Name        string      `json:"name"`
				RatingValue json.Number `json:"ratingValue"`
			} `json:"aggregateRating"`
		}
		if err := json.Unmarshal(m[1], &obj); err != nil {
			continue
		}
		if obj.AggregateRating.Name != "Metascore" || obj.AggregateRating.RatingValue == "" {
			continue
		}
		f, err := obj.AggregateRating.RatingValue.Float64()
		if err != nil {
			continue
		}
		return int(math.Round(f)), true, nil
	}
	return 0, false, nil
}
