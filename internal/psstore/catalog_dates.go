package psstore

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	playStationBlogBase  = "https://blog.playstation.com"
	maxSitemapBytes      = 4 << 20
	maxAnnouncementBytes = 6 << 20
	// CatalogAnnouncementParserVersion инвалидирует SQLite-кэш после изменения
	// правил разбора уже известных страниц.
	CatalogAnnouncementParserVersion = 1
)

// AnnouncementRef — страница официального анонса из sitemap PlayStation Blog.
type AnnouncementRef struct {
	URL          string
	LastModified string
}

// CatalogAddition — объявленная дата появления одной игры в Game Catalog.
type CatalogAddition struct {
	Title     string
	AddedOn   time.Time
	SourceURL string
}

// CatalogAnnouncement — разобранный официальный анонс Game Catalog.
type CatalogAnnouncement struct {
	URL          string
	LastModified string
	PublishedOn  time.Time
	Games        []CatalogAddition
}

type sitemapURLSet struct {
	URLs []struct {
		Loc     string `xml:"loc"`
		LastMod string `xml:"lastmod"`
	} `xml:"url"`
}

type sitemapIndexSet struct {
	Sitemaps []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
}

// FetchAnnouncementIndex получает ссылки на анонсы с запуска нового PS Plus в
// 2022 году. Годовые sitemap дают историю, recent — свежие записи текущего года.
func FetchAnnouncementIndex(ctx context.Context, client *http.Client, currentYear int) ([]AnnouncementRef, error) {
	if currentYear < 2022 {
		currentYear = 2022
	}
	indexURL := playStationBlogBase + "/wp-sitemap.xml"
	indexRaw, err := fetchBlogDocument(ctx, client, indexURL, maxSitemapBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch announcement sitemap index %s: %w", indexURL, err)
	}
	sitemapURLs, err := parseAnnouncementSitemapIndex(indexRaw, currentYear)
	if err != nil {
		return nil, fmt.Errorf("parse announcement sitemap index %s: %w", indexURL, err)
	}

	byURL := make(map[string]AnnouncementRef)
	for _, sitemapURL := range sitemapURLs {
		raw, err := fetchBlogDocument(ctx, client, sitemapURL, maxSitemapBytes)
		if err != nil {
			return nil, fmt.Errorf("fetch announcement sitemap %s: %w", sitemapURL, err)
		}
		refs, err := ParseAnnouncementSitemap(raw)
		if err != nil {
			return nil, fmt.Errorf("parse announcement sitemap %s: %w", sitemapURL, err)
		}
		for _, ref := range refs {
			if old, ok := byURL[ref.URL]; !ok || ref.LastModified > old.LastModified {
				byURL[ref.URL] = ref
			}
		}
	}

	out := make([]AnnouncementRef, 0, len(byURL))
	for _, ref := range byURL {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out, nil
}

func parseAnnouncementSitemapIndex(raw []byte, currentYear int) ([]string, error) {
	if currentYear < 2022 {
		currentYear = 2022
	}
	var index sitemapIndexSet
	if err := xml.Unmarshal(raw, &index); err != nil {
		return nil, err
	}

	const prefix = playStationBlogBase + "/wp-sitemap-posts-"
	seen := make(map[string]bool)
	var out []string
	for _, item := range index.Sitemaps {
		loc := strings.TrimSpace(item.Loc)
		if !strings.HasPrefix(loc, prefix) || !strings.HasSuffix(loc, ".xml") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(loc, prefix), ".xml")
		if name != "recent" {
			year, err := strconv.Atoi(name)
			if err != nil || year < 2022 || year > currentYear {
				continue
			}
		}
		if !seen[loc] {
			seen[loc] = true
			out = append(out, loc)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("post sitemaps for catalog history not found")
	}
	sort.Strings(out)
	return out, nil
}

// ParseAnnouncementSitemap оставляет только записи, способные содержать состав
// Game Catalog. Рекламные подборки уже доступных игр отбрасываются.
func ParseAnnouncementSitemap(raw []byte) ([]AnnouncementRef, error) {
	var set sitemapURLSet
	if err := xml.Unmarshal(raw, &set); err != nil {
		return nil, err
	}
	var out []AnnouncementRef
	for _, item := range set.URLs {
		if !isCatalogAnnouncementURL(item.Loc) {
			continue
		}
		out = append(out, AnnouncementRef{
			URL:          strings.TrimSpace(item.Loc),
			LastModified: strings.TrimSpace(item.LastMod),
		})
	}
	return out, nil
}

func isCatalogAnnouncementURL(rawURL string) bool {
	u := strings.ToLower(rawURL)
	if strings.Contains(u, "must-play") || strings.Contains(u, "guide-to") {
		return false
	}
	return strings.Contains(u, "playstation-plus-game-catalog-for") ||
		strings.Contains(u, "playstation-plus-game-catalog-lineup") ||
		strings.Contains(u, "playstation-plus-game-catalog-classics-for") ||
		strings.Contains(u, "monthly-games-and-game-catalog-lineup") ||
		strings.Contains(u, "all-new-playstation-plus-game-lineup")
}

// FetchCatalogAnnouncement загружает и разбирает одну страницу анонса.
func FetchCatalogAnnouncement(ctx context.Context, client *http.Client, ref AnnouncementRef) (CatalogAnnouncement, error) {
	raw, err := fetchBlogDocument(ctx, client, ref.URL, maxAnnouncementBytes)
	if err != nil {
		return CatalogAnnouncement{}, err
	}
	return ParseCatalogAnnouncement(raw, ref)
}

func fetchBlogDocument(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		raw, retry, err := fetchBlogDocumentOnce(ctx, client, rawURL, limit)
		if err == nil {
			return raw, nil
		}
		lastErr = err
		if !retry || attempt == 2 {
			break
		}
		delay := time.Duration(attempt+1) * 500 * time.Millisecond
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, lastErr
}

func fetchBlogDocumentOnce(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "ps-plus-catalog/1.0")
	req.Header.Set("Accept", "text/html,application/xml;q=0.9,*/*;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return nil, retry, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, true, err
	}
	if int64(len(raw)) > limit {
		return nil, false, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return raw, false, nil
}

var (
	publishedRE = regexp.MustCompile(`(?is)<time[^>]*class="[^"]*\bentry-date\b[^"]*\bpublished\b[^"]*"[^>]*datetime="([^"]+)"`)
	contentRE   = regexp.MustCompile(`(?is)<div[^>]*class="[^"]*\bentry-content\b[^"]*"[^>]*>(.*?)(?:<div[^>]*class="[^"]*\bpost-single__footer\b|</article>)`)
	blockRE     = regexp.MustCompile(`(?is)<(h[1-4]|p|li)\b[^>]*>.*?</(?:h[1-4]|p|li)>`)
	tagRE       = regexp.MustCompile(`(?is)<[^>]+>`)
	spaceRE     = regexp.MustCompile(`\s+`)
	monthDayRE  = regexp.MustCompile(`(?i)\b(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{1,2})(?:st|nd|rd|th)?\b`)
)

type articleBlock struct {
	tag  string
	text string
	raw  string
}

// ParseCatalogAnnouncement разбирает дату для региона TR и игры секции Extra.
// Даты, явно предназначенные только для US/UK/Japan, не применяются к Турции.
func ParseCatalogAnnouncement(raw []byte, ref AnnouncementRef) (CatalogAnnouncement, error) {
	page := string(raw)
	published, err := parsePublishedDate(page)
	if err != nil {
		return CatalogAnnouncement{}, err
	}
	contentMatch := contentRE.FindStringSubmatch(page)
	if len(contentMatch) != 2 {
		return CatalogAnnouncement{}, fmt.Errorf("entry content not found")
	}
	blocks := parseArticleBlocks(contentMatch[1])
	if len(blocks) == 0 {
		return CatalogAnnouncement{}, fmt.Errorf("article has no content blocks")
	}

	defaultDate, ok := defaultCatalogDate(blocks, published)
	if !ok {
		return CatalogAnnouncement{}, fmt.Errorf("catalog availability date not found")
	}
	titles := catalogGameTitles(blocks)
	if len(titles) == 0 {
		return CatalogAnnouncement{}, fmt.Errorf("Extra game list not found")
	}

	games := make([]CatalogAddition, 0, len(titles))
	seen := make(map[string]bool)
	for _, title := range titles {
		key := NormalizeCatalogTitle(title)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		addedOn := defaultDate
		if exception, found := gameSpecificDate(blocks, title, published); found {
			addedOn = exception
		}
		games = append(games, CatalogAddition{Title: title, AddedOn: addedOn, SourceURL: ref.URL})
	}
	return CatalogAnnouncement{
		URL:          ref.URL,
		LastModified: ref.LastModified,
		PublishedOn:  published,
		Games:        games,
	}, nil
}

func parsePublishedDate(page string) (time.Time, error) {
	m := publishedRE.FindStringSubmatch(page)
	if len(m) != 2 {
		return time.Time{}, fmt.Errorf("published date not found")
	}
	t, err := time.Parse(time.RFC3339, html.UnescapeString(m[1]))
	if err != nil {
		return time.Time{}, fmt.Errorf("parse published date: %w", err)
	}
	return t, nil
}

func parseArticleBlocks(content string) []articleBlock {
	matches := blockRE.FindAllString(content, -1)
	out := make([]articleBlock, 0, len(matches))
	for _, raw := range matches {
		openEnd := strings.IndexByte(raw, '>')
		if openEnd < 1 {
			continue
		}
		tag := strings.ToLower(strings.Fields(raw[1:openEnd])[0])
		text := cleanHTMLText(raw)
		if text != "" {
			out = append(out, articleBlock{tag: tag, text: text, raw: raw})
		}
	}
	return out
}

func cleanHTMLText(raw string) string {
	s := tagRE.ReplaceAllString(raw, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return strings.TrimSpace(spaceRE.ReplaceAllString(s, " "))
}

func defaultCatalogDate(blocks []articleBlock, published time.Time) (time.Time, bool) {
	type patternScore struct {
		re    *regexp.Regexp
		score int
	}
	// Приоритет отражает точность формулировки: явная дата полной линейки
	// (особенно all other regions) надёжнее общего упоминания available. Более
	// ранний блок служит только слабым tie-breaker для одинакового правила.
	const (
		fullLineupScore  = 120
		otherTitlesScore = 115
		asideScore       = 110
		allTitlesScore   = 100
		playableScore    = 95
		lineupScore      = 85
		fallbackScore    = 60
		europeScore      = 118
	)
	date := monthDayPattern()
	patterns := []patternScore{
		{regexp.MustCompile(`(?i)\bfull lineup\b.{0,180}?\bavailable\b.{0,80}?` + date), fullLineupScore},
		{regexp.MustCompile(`(?i)\ball other (?:games|titles)\b.{0,120}?\bavailable\b.{0,80}?` + date), otherTitlesScore},
		{regexp.MustCompile(`(?i)\baside from\b.{0,160}?\ball titles\b.{0,80}?\bavailable\b.{0,80}?` + date), asideScore},
		{regexp.MustCompile(`(?i)\ball (?:of )?(?:these )?(?:games|titles)\b.{0,120}?\bavailable\b.{0,80}?` + date), allTitlesScore},
		{regexp.MustCompile(`(?i)\b(?:all |these (?:and many more )?)?(?:games|titles|lineup)\b.{0,160}?\bplayable\b.{0,80}?` + date), playableScore},
		{regexp.MustCompile(`(?i)\blineup\b.{0,180}?\bavailable\b.{0,80}?` + date), lineupScore},
		{regexp.MustCompile(`(?i)\bavailable (?:to play )?(?:on |from |starting )?(?:Tuesday,? |Wednesday,? )?` + date), fallbackScore},
		{regexp.MustCompile(`(?i)\bplayable (?:on |from |starting )?(?:Tuesday,? |Wednesday,? )?` + date), fallbackScore},
		// Стартовая линейка 2022 года имела несколько региональных дат; Турция
		// относится к европейскому запуску.
		{regexp.MustCompile(`(?i)\bEurope\b.{0,100}?` + date), europeScore},
	}

	bestScore := -1
	var best time.Time
	for i, block := range blocks {
		for _, p := range patterns {
			m := p.re.FindStringSubmatch(block.text)
			if len(m) != 3 {
				continue
			}
			t, ok := dateFromParts(m[1], m[2], published)
			if !ok {
				continue
			}
			score := p.score - i/4
			if score > bestScore {
				best, bestScore = t, score
			}
		}
	}
	return best, bestScore >= 0
}

func monthDayPattern() string {
	return `\b(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{1,2})(?:st|nd|rd|th)?\b`
}

func dateFromParts(monthName, dayText string, published time.Time) (time.Time, bool) {
	month, ok := parseMonth(monthName)
	if !ok {
		return time.Time{}, false
	}
	day, err := strconv.Atoi(dayText)
	if err != nil || day < 1 || day > 31 {
		return time.Time{}, false
	}
	year := published.Year()
	if int(month) < int(published.Month())-6 {
		year++
	}
	t := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	return t, t.Month() == month && t.Day() == day
}

func parseMonth(name string) (time.Month, bool) {
	for m := time.January; m <= time.December; m++ {
		if strings.EqualFold(m.String(), name) {
			return m, true
		}
	}
	return 0, false
}

func catalogGameTitles(blocks []articleBlock) []string {
	inCatalog := false
	var out []string
	for _, block := range blocks {
		lower := strings.ToLower(block.text)
		if strings.Contains(lower, "games joining the playstation plus game catalog") {
			inCatalog = true
			continue
		}
		if inCatalog && strings.HasPrefix(lower, "classics catalog") {
			inCatalog = false
			continue
		}
		if strings.HasPrefix(block.tag, "h") {
			switch {
			case isExtraCatalogHeading(lower):
				inCatalog = true
				continue
			case inCatalog && isCatalogEndHeading(lower):
				inCatalog = false
				continue
			}
		}
		if !inCatalog {
			continue
		}
		if title := gameTitleFromBlock(block); title != "" {
			out = append(out, title)
		}
	}
	return out
}

func isExtraCatalogHeading(s string) bool {
	return strings.Contains(s, "extra and premium") ||
		strings.Contains(s, "extra & premium") ||
		strings.Contains(s, "game catalog | playstation plus extra") ||
		strings.Contains(s, "game catalog lineup for playstation plus extra") ||
		strings.Contains(s, "ps4 and ps5 game catalog")
}

func isCatalogEndHeading(s string) bool {
	return strings.Contains(s, "classic") ||
		strings.Contains(s, "time-limited game trials") ||
		strings.Contains(s, "original ps3") ||
		(strings.Contains(s, "premium") && !strings.Contains(s, "extra"))
}

func gameTitleFromBlock(block articleBlock) string {
	text := strings.TrimSpace(block.text)
	pipe := strings.Index(text, "|")
	if pipe >= 1 {
		right := strings.ToLower(text[pipe+1:])
		if !strings.Contains(right, "ps4") && !strings.Contains(right, "ps5") {
			return ""
		}
		text = text[:pipe]
	} else if block.tag == "li" {
		platformSuffix := regexp.MustCompile(`(?i)\s*\(\s*PS[45](?:\s*[/,&]\s*PS[45])?\s*\)(?:\s*\[[^]]+\])?\s*$`)
		text = platformSuffix.ReplaceAllString(text, "")
	} else if strings.HasPrefix(block.tag, "h") {
		lower := strings.ToLower(text)
		if lower == "download the image" || isExtraCatalogHeading(lower) || isCatalogEndHeading(lower) {
			return ""
		}
	} else {
		return ""
	}
	title := strings.TrimSpace(text)
	title = strings.TrimSpace(strings.TrimRight(title, "* "))
	if len([]rune(title)) < 2 || len([]rune(title)) > 140 {
		return ""
	}
	return title
}

func gameSpecificDate(blocks []articleBlock, title string, published time.Time) (time.Time, bool) {
	quoted := regexp.QuoteMeta(strings.TrimSpace(strings.TrimRight(title, "* ")))
	date := monthDayPattern()
	direct := regexp.MustCompile(`(?i)(?:^|[.!?]\s+|,\s*with\s+|\bwith\s+)[*\s]*` + quoted +
		`.{0,90}?\b(?:is available|will be available|available to play|will launch|launch(?:es|ing|ed))\b.{0,100}?` + date)
	released := regexp.MustCompile(`(?i)` + quoted +
		`.{0,90}?\b(?:which )?released\b.{0,100}?` + date)

	for _, block := range blocks {
		m := direct.FindStringSubmatch(block.text)
		if len(m) != 3 {
			m = released.FindStringSubmatch(block.text)
		}
		if len(m) != 3 {
			continue
		}
		lower := strings.ToLower(block.text)
		if (strings.Contains(lower, "in the us") || strings.Contains(lower, "in us") ||
			strings.Contains(lower, "uk and japan")) &&
			!strings.Contains(lower, "globally") && !strings.Contains(lower, "all other regions") {
			continue
		}
		if t, ok := dateFromParts(m[1], m[2], published); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

// NormalizeCatalogTitle приводит заголовки стора и блога к форме для
// сопоставления. Она не удаляет смысловые слова вроде "remastered".
func NormalizeCatalogTitle(s string) string {
	s = html.UnescapeString(s)
	if pipe := strings.Index(s, "|"); pipe >= 0 {
		s = s[:pipe]
	}
	s = strings.ToLower(strings.NewReplacer(
		"™", "", "®", "", "©", "",
		"’", "'", "‘", "'", "–", "-", "—", "-",
		"&", " and ",
	).Replace(s))
	var b strings.Builder
	space := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(spaceRE.ReplaceAllString(b.String(), " "))
}

// MatchCatalogAddition находит одно безопасное соответствие. found и ambiguous
// взаимоисключающие: false/false означает, что кандидата нет; false/true — что
// есть несколько близких, но неразличимых кандидатов. Повторные анонсы одной
// игры не считаются неоднозначностью: выбирается самая новая дата.
func MatchCatalogAddition(title, titleEn string, candidates []CatalogAddition) (match CatalogAddition, found, ambiguous bool) {
	targetKeys := titleMatchKeys(titleEn)
	for k := range titleMatchKeys(title) {
		targetKeys[k] = true
	}
	if len(targetKeys) == 0 {
		return CatalogAddition{}, false, false
	}

	type scored struct {
		c     CatalogAddition
		key   string
		score float64
	}
	var matches []scored
	for _, c := range candidates {
		best := 0.0
		bestKey := ""
		for ck := range titleMatchKeys(c.Title) {
			for tk := range targetKeys {
				score := titleSimilarity(tk, ck)
				if score > best {
					best, bestKey = score, ck
				}
			}
		}
		if best >= 0.86 {
			matches = append(matches, scored{c: c, key: bestKey, score: best})
		}
	}
	if len(matches) == 0 {
		return CatalogAddition{}, false, false
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		if !matches[i].c.AddedOn.Equal(matches[j].c.AddedOn) {
			return matches[i].c.AddedOn.After(matches[j].c.AddedOn)
		}
		return matches[i].c.Title < matches[j].c.Title
	})
	best := matches[0]
	if best.score == 1 {
		for _, m := range matches[1:] {
			if m.score != 1 {
				break
			}
			if NormalizeCatalogTitle(m.c.Title) == NormalizeCatalogTitle(best.c.Title) &&
				m.c.AddedOn.After(best.c.AddedOn) {
				best = m
			}
		}
		return best.c, true, false
	}
	for _, other := range matches[1:] {
		if other.score < best.score-0.08 {
			break
		}
		if other.key != best.key && NormalizeCatalogTitle(other.c.Title) != NormalizeCatalogTitle(best.c.Title) {
			return CatalogAddition{}, false, true
		}
	}
	// Для повторного добавления берём самый свежий анонс того же заголовка.
	for _, m := range matches {
		if m.key == best.key && m.c.AddedOn.After(best.c.AddedOn) {
			best = m
		}
	}
	return best.c, true, false
}

func titleMatchKeys(s string) map[string]bool {
	full := NormalizeCatalogTitle(s)
	keys := make(map[string]bool)
	if full == "" {
		return keys
	}
	keys[full] = true
	words := strings.Fields(full)
	drop := map[string]bool{"ps4": true, "ps5": true}
	var noPlatform []string
	for _, word := range words {
		if !drop[word] {
			noPlatform = append(noPlatform, word)
		}
	}
	trimmed := strings.Join(noPlatform, " ")
	trimmed = strings.TrimSuffix(trimmed, " and")
	if trimmed != "" {
		keys[trimmed] = true
	}
	for _, suffix := range []string{
		" standard edition", " digital deluxe edition", " deluxe edition",
		" ultimate edition", " complete edition", " game of the year edition",
	} {
		if base := strings.TrimSuffix(trimmed, suffix); base != trimmed && base != "" {
			keys[base] = true
		}
	}
	return keys
}

func titleSimilarity(a, b string) float64 {
	if a == b {
		return 1
	}
	aw, bw := strings.Fields(a), strings.Fields(b)
	count := make(map[string]int)
	for _, w := range aw {
		count[w]++
	}
	common := 0
	for _, w := range bw {
		if count[w] > 0 {
			common++
			count[w]--
		}
	}
	if len(aw)+len(bw) == 0 {
		return 0
	}
	return 2 * float64(common) / float64(len(aw)+len(bw))
}
