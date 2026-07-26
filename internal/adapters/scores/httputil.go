package scores

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Лимиты на размер тела ответа внешних сервисов: защита от ошибочного,
// изменившегося или скомпрометированного endpoint'а, способного отдать
// гигантский ответ и исчерпать память. JSON-ответы (OpenCritic, HLTB) малы;
// HTML-страница Metacritic заметно крупнее, поэтому лимит для неё отдельный.
const (
	maxJSONBytes = 8 << 20  // 8 MiB
	maxHTMLBytes = 16 << 20 // 16 MiB
)

// readLimited читает не более limit байт из r и возвращает явную ошибку, если
// тело ответа превышает лимит (а не молча обрезает его).
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("ответ превышает лимит %d байт", limit)
	}
	return body, nil
}

// doTrustedRequest выполняет запрос только к явно разрешённым HTTPS-хостам и
// применяет то же правило ко всей цепочке перенаправлений. Это не позволяет
// внешнему сервису увести запрос с ключом или токеном на посторонний адрес.
func doTrustedRequest(client *http.Client, request *http.Request, allowedHosts ...string) (*http.Response, error) {
	if err := validateTrustedHTTPSURL(request.URL, allowedHosts...); err != nil {
		return nil, err
	}
	copyClient := *client
	originalRedirect := client.CheckRedirect
	copyClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if err := validateTrustedHTTPSURL(next.URL, allowedHosts...); err != nil {
			return err
		}
		if originalRedirect != nil {
			return originalRedirect(next, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("слишком много HTTP-переходов")
		}
		return nil
	}
	return copyClient.Do(request)
}

func validateTrustedHTTPSURL(parsed *url.URL, allowedHosts ...string) error {
	if parsed == nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return fmt.Errorf("недоверенный URL внешнего источника: %q", parsed)
	}
	for _, allowedHost := range allowedHosts {
		if strings.EqualFold(parsed.Hostname(), allowedHost) {
			return nil
		}
	}
	return fmt.Errorf("недоверенный URL внешнего источника: %q", parsed)
}
