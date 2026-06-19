package scores

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	hltbBase   = "https://howlongtobeat.com"
	hltbUA     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"
	hltbSecCHUA = `"Not/A)Brand";v="99", "Chromium";v="148"`
)

// HLTBResult — данные одной игры с HowLongToBeat.
type HLTBResult struct {
	MainExtraSeconds int // время Main + Sides (comp_plus), 0 если неизвестно
	Rating           int // пользовательский рейтинг (review_score), 0 если неизвестно
}

// HLTBSession держит honeypot-токен поиска и переиспользует его между запросами,
// переполучая при сбое. HLTB защищает /api/bleed handshake'ом, поэтому перед
// поиском нужен GET /api/bleed/init.
type HLTBSession struct {
	client              *http.Client
	token, hpKey, hpVal string
}

// NewHLTBSession создаёт сессию поверх http-клиента.
func NewHLTBSession(c *http.Client) *HLTBSession { return &HLTBSession{client: c} }

// Lookup ищет игру по названию и возвращает время Main+Sides и рейтинг.
// HLTB-поиск чувствителен к лишним словам (издания/платформы/подзаголовки дают
// пустую выдачу), поэтому пробуем несколько вариантов запроса от полного к ядру.
// found=false, если совпадений нет ни по одному варианту.
func (s *HLTBSession) Lookup(ctx context.Context, title string) (HLTBResult, bool, error) {
	for _, terms := range hltbCandidates(title) {
		data, err := s.searchWithRetry(ctx, terms)
		if err != nil {
			return HLTBResult{}, false, err
		}
		if g, ok := bestHLTB(data, title); ok {
			return HLTBResult{MainExtraSeconds: g.CompPlus, Rating: g.ReviewScore}, true, nil
		}
	}
	return HLTBResult{}, false, nil
}

// searchWithRetry делает поиск, переполучая токен при протухании.
func (s *HLTBSession) searchWithRetry(ctx context.Context, terms []string) ([]hltbGame, error) {
	data, err := s.search(ctx, terms)
	if err == errHLTBAuth {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
		data, err = s.search(ctx, terms)
	}
	return data, err
}

var errHLTBAuth = fmt.Errorf("hltb: auth/handshake required")

func (s *HLTBSession) init(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/bleed/init?t=%d", hltbBase, time.Now().UnixMilli())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	s.setBrowserHeaders(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("hltb init: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hltb init: status %d", resp.StatusCode)
	}
	var v struct {
		Token string `json:"token"`
		HPKey string `json:"hpKey"`
		HPVal string `json:"hpVal"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return fmt.Errorf("hltb init decode: %w", err)
	}
	s.token, s.hpKey, s.hpVal = v.Token, v.HPKey, v.HPVal
	return nil
}

// hltbGame — игра в ответе HLTB.
type hltbGame struct {
	GameName    string `json:"game_name"`
	CompPlus    int    `json:"comp_plus"`
	ReviewScore int    `json:"review_score"`
}

// search выполняет POST /api/bleed с заданными поисковыми словами. Возвращает
// errHLTBAuth при 401/403/404 (признак протухшего/отсутствующего токена).
func (s *HLTBSession) search(ctx context.Context, terms []string) ([]hltbGame, error) {
	if s.token == "" {
		return nil, errHLTBAuth
	}
	payload := map[string]any{
		"searchType":  "games",
		"searchTerms": terms,
		"searchPage":  1,
		"size":        20,
		"searchOptions": map[string]any{
			"games": map[string]any{
				"userId": 0, "platform": "", "sortCategory": "popular", "rangeCategory": "main",
				"rangeTime": map[string]any{"min": nil, "max": nil},
				"gameplay":  map[string]any{"perspective": "", "flow": "", "genre": "", "difficulty": ""},
				"rangeYear": map[string]any{"min": "", "max": ""},
				"modifier":  "",
			},
			"users":      map[string]any{"sortCategory": "postcount"},
			"lists":      map[string]any{"sortCategory": "follows"},
			"filter":     "", "sort": 0, "randomizer": 0,
		},
		"useCache": true,
		s.hpKey:    s.hpVal,
	}
	raw, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, hltbBase+"/api/bleed", bytes.NewReader(raw))
	s.setBrowserHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-auth-token", s.token)
	req.Header.Set("x-hp-key", s.hpKey)
	req.Header.Set("x-hp-val", s.hpVal)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hltb search: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404:
		s.token = "" // протух
		return nil, errHLTBAuth
	default:
		return nil, fmt.Errorf("hltb search: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []hltbGame `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("hltb parse: %w", err)
	}
	return r.Data, nil
}

// bestHLTB выбирает совпадение консервативно: точное по нормализованному имени,
// иначе имя результата — подстрока нашего названия (или наоборот). Иначе — нет.
func bestHLTB(data []hltbGame, title string) (hltbGame, bool) {
	if len(data) == 0 {
		return hltbGame{}, false
	}
	want := NormalizeTitle(title)
	for _, g := range data {
		if NormalizeTitle(g.GameName) == want {
			return g, true
		}
	}
	for _, g := range data {
		ng := NormalizeTitle(g.GameName)
		if ng != "" && (strings.Contains(want, ng) || strings.Contains(ng, want)) {
			return g, true
		}
	}
	return hltbGame{}, false
}

// hltbCandidates строит варианты поисковых слов от полного очищенного названия к
// ядру (первые 3, первые 2 слова) — HLTB на переусложнённый запрос даёт пусто.
func hltbCandidates(title string) [][]string {
	words := strings.Fields(hltbClean(title))
	if len(words) == 0 {
		words = strings.Fields(title)
	}
	cands := [][]string{words}
	if len(words) > 3 {
		cands = append(cands, words[:3])
	}
	if len(words) > 2 {
		cands = append(cands, words[:2])
	}
	return cands
}

// hltbStrip убирает скобочный контент [], () и шум изданий/платформ/префиксов
// агрессивнее, чем CleanTitle (для HLTB-поиска, не для slug Metacritic).
var (
	hltbBrackets = regexp.MustCompile(`[\(\[][^\)\]]*[\)\]]`)
	hltbEdition  = regexp.MustCompile(`(?i)\b(standard|classic|enhanced|legendary|extended|complete|definitive|deluxe|ultimate|gold|premium|digital|next\s*gen|cross[- ]gen|elite|game of the year|goty|console|anniversary|jumbo|collector'?s|legacy)\b[^|]*?\b(edition|bundle|collection|version|set|upgrade|pass)\b`)
	hltbExtra    = regexp.MustCompile(`(?i)\b(free upgrade|expansion pass|cross[- ]gen|next gen|playstation plus|directors? cut|ea sports|remastered|reforged|ps4|ps5|playstation\s*[45]|ps\s*vr2?)\b`)
)

func hltbClean(s string) string {
	s = strings.NewReplacer("’", "", "'", "", "®", " ", "™", " ", "|", " ", ":", " ", "-", " ", "–", " ", "—", " ", "&", " ", "+", " ").Replace(s)
	s = diacritics.Replace(s)
	s = hltbBrackets.ReplaceAllString(s, " ")
	s = hltbEdition.ReplaceAllString(s, " ")
	s = hltbExtra.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func (s *HLTBSession) setBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", hltbUA)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Origin", hltbBase)
	req.Header.Set("Referer", hltbBase+"/")
	req.Header.Set("sec-ch-ua", hltbSecCHUA)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"macOS"`)
	req.Header.Set("sec-fetch-site", "same-origin")
	req.Header.Set("sec-fetch-mode", "cors")
	req.Header.Set("sec-fetch-dest", "empty")
}
