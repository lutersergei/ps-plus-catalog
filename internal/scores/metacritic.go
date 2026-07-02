package scores

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/url"
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
var mcSearchGameURLRe = regexp.MustCompile(`/game/([a-z0-9][a-z0-9-]*)/`)

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
	PageURL string
	UserErr error

	pageTitle string
	pageHTML  []byte
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
	seen := map[string]bool{}
	for _, slug := range metacriticSlugCandidates(titleEn) {
		seen[slug] = true
		res, found, err := metacriticScoresBySlug(ctx, c, slug, true)
		if err != nil {
			return MetacriticResult{}, err
		}
		if found {
			return res, nil
		}
	}
	searchSlugs, err := metacriticSearchSlugs(ctx, c, titleEn)
	if err != nil {
		return MetacriticResult{}, err
	}
	for _, slug := range searchSlugs {
		if seen[slug] {
			continue
		}
		seen[slug] = true
		res, found, err := metacriticScoresBySlug(ctx, c, slug, false)
		if err != nil {
			return MetacriticResult{}, err
		}
		if !found || !metacriticTitlesMatch(titleEn, res.pageTitle) {
			continue
		}
		metacriticFillUserScore(ctx, c, slug, res.pageHTML, &res)
		return res, nil
	}
	return MetacriticResult{}, nil
}

func metacriticSlugCandidates(titleEn string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(slug string) {
		if slug == "" || seen[slug] {
			return
		}
		seen[slug] = true
		out = append(out, slug)
	}
	add(Slugify(metacriticSlugTitle(titleEn)))
	add(Slugify(titleEn))
	add(Slugify(CleanTitle(titleEn)))
	return out
}

func metacriticSlugTitle(title string) string {
	title = strings.NewReplacer("’", "", "'", "", "®", " ", "™", " ").Replace(title)
	title = strings.NewReplacer("FARCRY", "Far Cry", "Farcry", "Far Cry", "farcry", "far cry").Replace(title)
	return strings.Join(strings.Fields(title), " ")
}

// MetacriticSlug возвращает первый slug-кандидат для прямой ссылки на страницу
// игры. Порядок совпадает с MetacriticScores: сначала исходное название, потом
// очищенный fallback.
func MetacriticSlug(titleEn string) string {
	candidates := metacriticSlugCandidates(titleEn)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func metacriticScoresBySlug(ctx context.Context, c *http.Client, slug string, fetchUser bool) (MetacriticResult, bool, error) {
	pageURL := "https://www.metacritic.com/game/" + slug + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return MetacriticResult{}, false, err
	}
	req.Header.Set("User-Agent", mcUserAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.Do(req)
	if err != nil {
		return MetacriticResult{}, false, fmt.Errorf("metacritic fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return MetacriticResult{}, false, nil // игры нет под таким slug
	}
	if resp.StatusCode != http.StatusOK {
		return MetacriticResult{}, false, fmt.Errorf("metacritic status %d", resp.StatusCode)
	}
	body, err := readLimited(resp.Body, maxHTMLBytes)
	if err != nil {
		return MetacriticResult{}, false, fmt.Errorf("metacritic body: %w", err)
	}
	score, found, err := parseMetacritic(body)
	if err != nil {
		return MetacriticResult{}, false, err
	}
	res := MetacriticResult{PageURL: pageURL, pageTitle: parseMetacriticTitle(body), pageHTML: body}
	if found {
		res.Critic = Rating{Score: score, Found: true}
	}
	if fetchUser {
		metacriticFillUserScore(ctx, c, slug, body, &res)
	}
	return res, true, nil
}

func metacriticFillUserScore(ctx context.Context, c *http.Client, slug string, pageHTML []byte, res *MetacriticResult) {
	if res == nil {
		return
	}
	userURL := metacriticUserStatsURL(pageHTML, slug)
	user, err := metacriticUserScore(ctx, c, userURL, slug)
	if err != nil {
		res.UserErr = err
	} else {
		res.User = user
	}
}

func metacriticSearchSlugs(ctx context.Context, c *http.Client, titleEn string) ([]string, error) {
	searchURL := "https://www.metacritic.com/search/" + url.PathEscape(titleEn) + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", mcUserAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("metacritic search fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metacritic search status %d", resp.StatusCode)
	}
	body, err := readLimited(resp.Body, maxHTMLBytes)
	if err != nil {
		return nil, fmt.Errorf("metacritic search body: %w", err)
	}
	return parseMetacriticSearchSlugs(body, titleEn), nil
}

func parseMetacriticSearchSlugs(body []byte, titleEn string) []string {
	skip := map[string]bool{
		"all":           true,
		"ps5":           true,
		"ps4":           true,
		"xbox-series-x": true,
		"xbox-one":      true,
		"pc":            true,
		"switch":        true,
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range mcSearchGameURLRe.FindAllSubmatch(body, -1) {
		slug := string(m[1])
		if skip[slug] || seen[slug] || !metacriticSearchSlugMatches(titleEn, slug) {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
		if len(out) >= 40 {
			break
		}
	}
	return out
}

func metacriticSearchSlugMatches(title, slug string) bool {
	if title == "" {
		return true
	}
	want := metacriticTitleTokens(title)
	got := metacriticTitleTokens(strings.ReplaceAll(slug, "-", " "))
	if len(want) == 0 || len(got) == 0 {
		return false
	}
	return tokenSetSubset(want, got) || tokenSetSubset(got, want)
}

// parseMetacritic извлекает Metascore: приоритетно из JSON-LD (authoritative;
// на больших страницах разметка содержит и оценки отдельных рецензий). Если в
// JSON-LD оценки нет — запасной путь из разметки страницы.
func parseMetacritic(html []byte) (int, bool, error) {
	for _, m := range ldJSONRe.FindAllSubmatch(html, -1) {
		if score, ok := metacriticScoreFromJSONLD(m[1]); ok {
			return score, true, nil
		}
	}
	// запас: оценка в разметке (часть страниц без aggregateRating в JSON-LD)
	if m := metascoreRe.FindSubmatch(html); m != nil {
		if n, err := strconv.Atoi(string(m[1])); err == nil {
			return n, true, nil
		}
	}
	return 0, false, nil
}

func parseMetacriticTitle(html []byte) string {
	for _, m := range ldJSONRe.FindAllSubmatch(html, -1) {
		if title := metacriticTitleFromJSONLD(m[1]); title != "" {
			return title
		}
	}
	return ""
}

func metacriticTitleFromJSONLD(raw []byte) string {
	var data any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return ""
	}
	return metacriticTitleFromJSONValue(data)
}

func metacriticTitleFromJSONValue(v any) string {
	switch x := v.(type) {
	case map[string]any:
		if typ, _ := x["@type"].(string); typ == "VideoGame" {
			if name, _ := x["name"].(string); name != "" {
				return html.UnescapeString(name)
			}
		}
		for _, child := range x {
			if title := metacriticTitleFromJSONValue(child); title != "" {
				return title
			}
		}
	case []any:
		for _, child := range x {
			if title := metacriticTitleFromJSONValue(child); title != "" {
				return title
			}
		}
	}
	return ""
}

func metacriticTitlesMatch(want, got string) bool {
	wantTokens := metacriticTitleTokens(want)
	gotTokens := metacriticTitleTokens(got)
	if len(wantTokens) == 0 || len(gotTokens) == 0 {
		return false
	}
	if tokenSetsEqual(wantTokens, gotTokens) {
		return true
	}
	if tokenSetSubset(wantTokens, gotTokens) {
		return true
	}
	if tokenSetSubset(gotTokens, wantTokens) && onlyGenericExtras(wantTokens, gotTokens) {
		return true
	}
	return false
}

func metacriticTitleTokens(s string) map[string]bool {
	s = strings.NewReplacer("40 000", "40000", "40,000", "40000").Replace(matchClean(s))
	words := strings.Fields(NormalizeTitle(s))
	out := map[string]bool{}
	for i := 0; i < len(words); i++ {
		if len(words[i]) == 1 {
			var acronym strings.Builder
			j := i
			for ; j < len(words) && len(words[j]) == 1; j++ {
				acronym.WriteString(words[j])
			}
			if acronym.Len() > 1 {
				out[acronym.String()] = true
				i = j - 1
				continue
			}
		}
		w := metacriticNormalizeToken(words[i])
		if w == "" || metacriticStopToken(w) {
			continue
		}
		if w == "40" && i+1 < len(words) && words[i+1] == "000" {
			out["40000"] = true
			i++
			continue
		}
		out[w] = true
	}
	return out
}

func metacriticNormalizeToken(s string) string {
	switch s {
	case "i":
		return "1"
	case "ii":
		return "2"
	case "iii":
		return "3"
	case "iv":
		return "4"
	case "v":
		return "5"
	case "vi":
		return "6"
	default:
		return s
	}
}

func metacriticStopToken(s string) bool {
	switch s {
	case "the", "official", "game", "fia", "ea", "sports":
		return true
	default:
		return false
	}
}

func tokenSetsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	return tokenSetSubset(a, b)
}

func tokenSetSubset(a, b map[string]bool) bool {
	for token := range a {
		if !b[token] {
			return false
		}
	}
	return true
}

func onlyGenericExtras(a, b map[string]bool) bool {
	for token := range a {
		if !b[token] && !metacriticStopToken(token) {
			return false
		}
	}
	return true
}

func metacriticScoreFromJSONLD(raw []byte) (int, bool) {
	var data any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return 0, false
	}
	return metacriticScoreFromJSONValue(data)
}

func metacriticScoreFromJSONValue(v any) (int, bool) {
	switch x := v.(type) {
	case map[string]any:
		if score, ok := metacriticScoreFromAggregateRating(x["aggregateRating"]); ok {
			return score, true
		}
		for _, child := range x {
			if score, ok := metacriticScoreFromJSONValue(child); ok {
				return score, true
			}
		}
	case []any:
		for _, child := range x {
			if score, ok := metacriticScoreFromJSONValue(child); ok {
				return score, true
			}
		}
	}
	return 0, false
}

func metacriticScoreFromAggregateRating(v any) (int, bool) {
	rating, ok := v.(map[string]any)
	if !ok {
		return 0, false
	}
	name, _ := rating["name"].(string)
	if name != "Metascore" {
		return 0, false
	}
	score, ok := jsonNumberToFloat(rating["ratingValue"])
	if !ok || math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 100 {
		return 0, false
	}
	return int(math.Round(score)), true
}

func jsonNumberToFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	default:
		return 0, false
	}
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
