package psstore

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// catalogURL — публичный JSON-эндпоинт со всем каталогом PS Plus (регион TR).
const catalogURL = "https://www.playstation.com/bin/imagic/gameslist?locale=tr-tr&categoryList=plus-games-list"

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"

// FetchCatalog загружает и разбирает весь каталог игр PS Plus Extra (TR).
func FetchCatalog(ctx context.Context, client *http.Client) ([]Game, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", "https://www.playstation.com/tr-tr/ps-plus/games/")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch catalog: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read catalog body: %w", err)
	}
	return parseGamesList(body)
}
