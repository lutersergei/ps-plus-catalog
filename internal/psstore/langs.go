package psstore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

const maxConceptBytes = 16 << 20 // 16 МБ

var (
	reNextData = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__" type="application/json">(.*?)</script>`)
	reApollo   = regexp.MustCompile(`(?s)<script[^>]*type="application/json"[^>]*>(.*?)</script>`)
)

// FetchLangs получает коды языков озвучки и субтитров для игры со страницы PS Store.
// conceptURL — значение store_url из каталога (https://store.playstation.com/tr-tr/concept/…).
// Возвращает пустые срезы, если языковых данных нет; ошибку — только при сетевом/парсер-сбое.
func FetchLangs(ctx context.Context, client *http.Client, conceptURL string) (spoken, screen []string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, conceptURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("concept page %s: HTTP %d", conceptURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxConceptBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("concept page %s: read body: %w", conceptURL, err)
	}
	if int64(len(body)) > maxConceptBytes {
		return nil, nil, fmt.Errorf("concept page %s: ответ > %d МБ", conceptURL, maxConceptBytes>>20)
	}

	// Первый уровень: __NEXT_DATA__ → props.pageProps.batarangs["info"].text
	m := reNextData.FindSubmatch(body)
	if m == nil {
		return nil, nil, fmt.Errorf("concept page %s: __NEXT_DATA__ не найден", conceptURL)
	}

	var nextData struct {
		Props struct {
			PageProps struct {
				Batarangs map[string]struct {
					Text string `json:"text"`
				} `json:"batarangs"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal(m[1], &nextData); err != nil {
		return nil, nil, fmt.Errorf("concept page %s: parse __NEXT_DATA__: %w", conceptURL, err)
	}

	info, ok := nextData.Props.PageProps.Batarangs["info"]
	if !ok || info.Text == "" {
		return []string{}, []string{}, nil
	}

	// Второй уровень: Apollo cache внутри HTML-фрагмента batarangs["info"].text
	// Ищем <script type="application/json"> с ключами Product:*
	seenSpoken := map[string]bool{}
	seenScreen := map[string]bool{}

	for _, sm := range reApollo.FindAllSubmatch([]byte(info.Text), -1) {
		var apolloData struct {
			Cache map[string]json.RawMessage `json:"cache"`
		}
		if err := json.Unmarshal(sm[1], &apolloData); err != nil {
			continue
		}
		hasProduct := false
		for key, raw := range apolloData.Cache {
			if !strings.HasPrefix(key, "Product:") {
				continue
			}
			hasProduct = true
			var product struct {
				SpokenLanguages []string `json:"spokenLanguages"`
				ScreenLanguages []string `json:"screenLanguages"`
			}
			if err := json.Unmarshal(raw, &product); err != nil {
				continue
			}
			for _, lang := range product.SpokenLanguages {
				seenSpoken[lang] = true
			}
			for _, lang := range product.ScreenLanguages {
				seenScreen[lang] = true
			}
		}
		if hasProduct {
			break // нашли нужный скрипт, не смотрим дальше
		}
	}

	spoken = make([]string, 0, len(seenSpoken))
	for lang := range seenSpoken {
		spoken = append(spoken, lang)
	}
	screen = make([]string, 0, len(seenScreen))
	for lang := range seenScreen {
		screen = append(screen, lang)
	}
	sort.Strings(spoken)
	sort.Strings(screen)
	return spoken, screen, nil
}
