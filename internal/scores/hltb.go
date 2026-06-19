package scores

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
// found=false, если совпадений нет.
func (s *HLTBSession) Lookup(ctx context.Context, title string) (HLTBResult, bool, error) {
	res, found, err := s.search(ctx, title)
	if err == errHLTBAuth { // токен протух/отсутствует — переполучаем и повторяем один раз
		if err := s.init(ctx); err != nil {
			return HLTBResult{}, false, err
		}
		res, found, err = s.search(ctx, title)
	}
	return res, found, err
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

// search выполняет POST /api/bleed. Возвращает errHLTBAuth при 401/403/404
// (признак протухшего/отсутствующего токена).
func (s *HLTBSession) search(ctx context.Context, title string) (HLTBResult, bool, error) {
	if s.token == "" {
		return HLTBResult{}, false, errHLTBAuth
	}
	payload := map[string]any{
		"searchType":  "games",
		"searchTerms": searchTerms(title),
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
		return HLTBResult{}, false, fmt.Errorf("hltb search: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404:
		s.token = "" // протух
		return HLTBResult{}, false, errHLTBAuth
	default:
		return HLTBResult{}, false, fmt.Errorf("hltb search: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return HLTBResult{}, false, err
	}
	return parseHLTB(body, title)
}

// parseHLTB выбирает лучшее совпадение по нормализованному названию и берёт
// comp_plus (Main+Sides) и review_score (рейтинг).
func parseHLTB(raw []byte, title string) (HLTBResult, bool, error) {
	var r struct {
		Data []struct {
			GameName    string `json:"game_name"`
			CompPlus    int    `json:"comp_plus"`
			ReviewScore int    `json:"review_score"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return HLTBResult{}, false, fmt.Errorf("hltb parse: %w", err)
	}
	if len(r.Data) == 0 {
		return HLTBResult{}, false, nil
	}
	want := NormalizeTitle(title)
	best := r.Data[0] // по умолчанию самый популярный
	for _, g := range r.Data {
		if NormalizeTitle(g.GameName) == want {
			best = g
			break
		}
	}
	return HLTBResult{MainExtraSeconds: best.CompPlus, Rating: best.ReviewScore}, true, nil
}

// searchTerms готовит поисковые слова: чистим название и режем по пробелам.
func searchTerms(title string) []string {
	fields := strings.Fields(CleanTitle(title))
	if len(fields) == 0 {
		fields = []string{title}
	}
	return fields
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
