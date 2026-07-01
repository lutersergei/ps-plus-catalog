package scores

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const mcUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var ldJSONRe = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)

// metascoreRe ловит оценку из разметки страницы (атрибут title/aria-label
// «Metascore N out of 100»). Нужна, т.к. на части страниц Metacritic
// JSON-LD не содержит aggregateRating, хотя оценка на странице есть.
var metascoreRe = regexp.MustCompile(`Metascore (\d{1,3}) out of 100`)
var mcUserStatsURLRe = regexp.MustCompile(`https://backend\.metacritic\.com/reviews/metacritic/user/games/[^"'<>\\]+/stats/web\?[^"'<>\\]+`)
var mcUserAnyURLRe = regexp.MustCompile(`https://backend\.metacritic\.com/reviews/metacritic/user/games/([^/"'<>\\]+)/`)

// Rating — оценка источника в шкале 0-100 и опциональное число голосов/рецензий.
type Rating struct {
	Score int
	Count int
	Found bool
}

// MetacriticResult содержит critic score и user score. UserErr не считается
// фатальным для critic score: sync может сохранить найденные данные и залогировать
// проблему пользовательской оценки отдельно.
type MetacriticResult struct {
	Critic  Rating
	User    Rating
	UserErr error
}

// MetacriticScore возвращает Metascore игры по её английскому названию.
// found=false, если страница недоступна, игра не найдена или нет рецензий.
func MetacriticScore(ctx context.Context, c *http.Client, titleEn string) (score int, found bool, err error) {
	res, err := MetacriticScores(ctx, c, titleEn)
	if err != nil {
		return 0, false, err
	}
	return res.Critic.Score, res.Critic.Found, nil
}

// MetacriticScores возвращает Metascore и User Score игры. User Score берётся из
// backend-компонента Metacritic и переводится из шкалы 0-10 в 0-100.
func MetacriticScores(ctx context.Context, c *http.Client, titleEn string) (MetacriticResult, error) {
	slug := Slugify(CleanTitle(titleEn))
	url := "https://www.metacritic.com/game/" + slug + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return MetacriticResult{}, err
	}
	req.Header.Set("User-Agent", mcUserAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.Do(req)
	if err != nil {
		return MetacriticResult{}, fmt.Errorf("metacritic fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return MetacriticResult{}, nil // игры нет под таким slug
	}
	if resp.StatusCode != http.StatusOK {
		return MetacriticResult{}, fmt.Errorf("metacritic status %d", resp.StatusCode)
	}
	body, err := readLimited(resp.Body, maxHTMLBytes)
	if err != nil {
		return MetacriticResult{}, fmt.Errorf("metacritic body: %w", err)
	}
	score, found, err := parseMetacritic(body)
	if err != nil {
		return MetacriticResult{}, err
	}
	res := MetacriticResult{}
	if found {
		res.Critic = Rating{Score: score, Found: true}
	}
	userURL := metacriticUserStatsURL(body, slug)
	user, err := metacriticUserScore(ctx, c, userURL, slug)
	if err != nil {
		res.UserErr = err
	} else {
		res.User = user
	}
	return res, nil
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

func metacriticUserStatsURL(pageHTML []byte, fallbackSlug string) string {
	page := html.UnescapeString(string(pageHTML))
	if m := mcUserStatsURLRe.FindString(page); m != "" {
		return strings.ReplaceAll(m, `\u0026`, "&")
	}
	if m := mcUserAnyURLRe.FindStringSubmatch(page); m != nil {
		return buildMetacriticUserStatsURL(m[1])
	}
	return buildMetacriticUserStatsURL(fallbackSlug)
}

func buildMetacriticUserStatsURL(slug string) string {
	return "https://backend.metacritic.com/reviews/metacritic/user/games/" + slug + "/stats/web?componentName=user-score-summary&componentDisplayName=User+Score+Summary&componentType=MetaScoreSummary"
}

func metacriticUserScore(ctx context.Context, c *http.Client, url, pageSlug string) (Rating, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Rating{}, err
	}
	req.Header.Set("User-Agent", mcUserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Origin", "https://www.metacritic.com")
	req.Header.Set("Referer", "https://www.metacritic.com/game/"+pageSlug+"/")
	resp, err := c.Do(req)
	if err != nil {
		return Rating{}, fmt.Errorf("metacritic user fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Rating{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return Rating{}, fmt.Errorf("metacritic user status %d", resp.StatusCode)
	}
	body, err := readLimited(resp.Body, maxJSONBytes)
	if err != nil {
		return Rating{}, fmt.Errorf("metacritic user body: %w", err)
	}
	score, count, found, err := parseMetacriticUserStats(body)
	if err != nil {
		return Rating{}, err
	}
	return Rating{Score: score, Count: count, Found: found}, nil
}

func parseMetacriticUserStats(raw []byte) (score int, count int, found bool, err error) {
	var data struct {
		Data struct {
			Item struct {
				Max         *float64 `json:"max"`
				Score       *float64 `json:"score"`
				ReviewCount int      `json:"reviewCount"`
			} `json:"item"`
		} `json:"data"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&data); err != nil {
		return 0, 0, false, fmt.Errorf("parse metacritic user stats: %w", err)
	}
	if data.Data.Item.Score == nil || data.Data.Item.Max == nil {
		return 0, 0, false, nil
	}
	max := *data.Data.Item.Max
	v := *data.Data.Item.Score
	if math.IsNaN(v) || math.IsInf(v, 0) || math.IsNaN(max) || math.IsInf(max, 0) || max <= 0 || v < 0 || v > max {
		return 0, 0, false, nil
	}
	return int(math.Round(v / max * 100)), data.Data.Item.ReviewCount, true, nil
}
