package scores

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
)

const mcUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var ldJSONRe = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)

// metascoreRe ловит оценку из разметки страницы (атрибут title/aria-label
// «Metascore N out of 100»). Нужна, т.к. на части страниц Metacritic
// JSON-LD не содержит aggregateRating, хотя оценка на странице есть.
var metascoreRe = regexp.MustCompile(`Metascore (\d{1,3}) out of 100`)

// MetacriticScore возвращает Metascore игры по её английскому названию.
// found=false, если страница недоступна, игра не найдена или нет рецензий.
func MetacriticScore(ctx context.Context, c *http.Client, titleEn string) (score int, found bool, err error) {
	url := "https://www.metacritic.com/game/" + Slugify(CleanTitle(titleEn)) + "/"
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
	body, err := readLimited(resp.Body, maxHTMLBytes)
	if err != nil {
		return 0, false, fmt.Errorf("metacritic body: %w", err)
	}
	return parseMetacritic(body)
}

// parseMetacritic извлекает Metascore: приоритетно из JSON-LD (authoritative;
// на больших страницах разметка содержит и оценки отдельных рецензий). Если в
// JSON-LD оценки нет — запасной путь из разметки страницы.
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
	// запас: оценка в разметке (часть страниц без aggregateRating в JSON-LD)
	if m := metascoreRe.FindSubmatch(html); m != nil {
		if n, err := strconv.Atoi(string(m[1])); err == nil {
			return n, true, nil
		}
	}
	return 0, false, nil
}
