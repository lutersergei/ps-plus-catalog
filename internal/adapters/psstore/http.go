package psstore

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// doTrustedRequest выполняет запрос и разрешает переходы только внутри явно
// заданного HTTPS-хоста. Копия клиента не меняет общий экземпляр из композиции.
func doTrustedRequest(client *http.Client, request *http.Request, allowedHost string) (*http.Response, error) {
	if err := validateTrustedHTTPSURL(request.URL, allowedHost); err != nil {
		return nil, err
	}
	copyClient := *client
	originalRedirect := client.CheckRedirect
	copyClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if err := validateTrustedHTTPSURL(next.URL, allowedHost); err != nil {
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

func validateTrustedHTTPSURL(parsed *url.URL, allowedHost string) error {
	if parsed == nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" ||
		!strings.EqualFold(parsed.Hostname(), allowedHost) {
		return fmt.Errorf("недоверенный URL внешнего источника: %q", parsed)
	}
	return nil
}
