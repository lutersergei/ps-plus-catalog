package scores

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	hltbBase    = "https://howlongtobeat.com"
	hltbUA      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"
	hltbSecCHUA = `"Not/A)Brand";v="99", "Chromium";v="148"`
)

// HLTBResult — данные одной игры с HowLongToBeat.
type HLTBResult struct {
	MainExtraSeconds int // время Main + Sides (comp_plus), 0 если неизвестно
	Rating           int // пользовательский рейтинг (review_score), 0 если неизвестно
	GameID           int
	PageURL          string
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
//
// found=false, если совпадений нет ни по одному варианту. Второй результат
// (conclusive) различает два вида промаха: true — HLTB вернул непустую выдачу,
// но нужной игры в ней нет (достоверно «нет на HLTB» — можно кэшировать); false —
// все варианты дали пустую выдачу (вероятен троттлинг — лучше перепроверить
// позже). При found=true conclusive тоже true.
func (s *HLTBSession) Lookup(ctx context.Context, title string) (res HLTBResult, found, conclusive bool, err error) {
	sawResults := false
	for _, terms := range hltbCandidates(title) {
		data, err := s.searchWithRetry(ctx, terms)
		if err != nil {
			return HLTBResult{}, false, false, err
		}
		if len(data) > 0 {
			sawResults = true
		}
		if g, ok := bestHLTB(data, title); ok {
			res := HLTBResult{MainExtraSeconds: g.CompPlus, Rating: g.ReviewScore, GameID: g.GameID}
			if g.GameID > 0 {
				res.PageURL = fmt.Sprintf("%s/game/%d", hltbBase, g.GameID)
			}
			return res, true, true, nil
		}
	}
	return HLTBResult{}, false, sawResults, nil
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("hltb init: %w", err)
	}
	s.setBrowserHeaders(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("hltb init: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hltb init: status %d", resp.StatusCode)
	}
	body, err := readLimited(resp.Body, maxJSONBytes)
	if err != nil {
		return fmt.Errorf("hltb init body: %w", err)
	}
	var v struct {
		Token string `json:"token"`
		HPKey string `json:"hpKey"`
		HPVal string `json:"hpVal"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return fmt.Errorf("hltb init decode: %w", err)
	}
	s.token, s.hpKey, s.hpVal = v.Token, v.HPKey, v.HPVal
	return nil
}

// hltbGame — игра в ответе HLTB.
type hltbGame struct {
	GameID      int    `json:"game_id"`
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
			"users":  map[string]any{"sortCategory": "postcount"},
			"lists":  map[string]any{"sortCategory": "follows"},
			"filter": "", "sort": 0, "randomizer": 0,
		},
		"useCache": true,
		s.hpKey:    s.hpVal,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("hltb payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hltbBase+"/api/bleed", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("hltb request: %w", err)
	}
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
	body, err := readLimited(resp.Body, maxJSONBytes)
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
// иначе совпадение по набору значимых токенов (TitlesMatch — все токены более
// короткого названия должны быть в более длинном, односложные требуют точного
// совпадения). Прежняя проверка через strings.Contains ловила чужие игры на
// коротких названиях и сиквелах. Иначе — совпадения нет.
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
		if TitlesMatch(title, g.GameName) {
			return g, true
		}
	}
	return hltbGame{}, false
}

// hltbCandidates строит варианты поисковых слов от полного очищенного названия к
// ядру (первые 3, первые 2 слова) — HLTB на переусложнённый запрос даёт пусто.
// Чистка названия — общая matchClean (см. normalize.go).
func hltbCandidates(title string) [][]string {
	words := strings.Fields(matchClean(title))
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
