package psstore

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// catalogURL — публичный JSON-эндпоинт со всем каталогом PS Plus (регион TR).
const catalogURL = "https://www.playstation.com/bin/imagic/gameslist?locale=tr-tr&categoryList=plus-games-list"

// maxCatalogBytes ограничивает размер тела каталога (реально ~0.5 MiB) — защита
// от изменившегося/ошибочного endpoint'а, способного отдать гигантский ответ.
const maxCatalogBytes = 64 << 20 // 64 MiB

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
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read catalog body: %w", err)
	}
	if int64(len(body)) > maxCatalogBytes {
		return nil, fmt.Errorf("read catalog body: ответ превышает лимит %d байт", maxCatalogBytes)
	}
	return parseGamesList(body)
}
