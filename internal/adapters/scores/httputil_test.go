package scores

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTrustedRequestRejectsUntrustedURLWithoutRequest(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})}
	request, err := http.NewRequest(http.MethodGet, "https://www.metacritic.com.evil.example/game/1", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, err := doTrustedRequest(client, request, metacriticWebHost)
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("недоверенный URL должен быть отклонён")
	}
	if requests != 0 {
		t.Fatalf("выполнено запросов: %d", requests)
	}
}

func TestTrustedRequestRejectsCrossHostRedirect(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://evil.example/internal"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})}
	request, err := http.NewRequest(http.MethodGet, "https://www.metacritic.com/game/1", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, err := doTrustedRequest(client, request, metacriticWebHost)
	if response != nil {
		response.Body.Close()
	}
	if err == nil || requests != 1 {
		t.Fatalf("response=%v err=%v requests=%d", response, err, requests)
	}
}

func TestTrustedRequestAllowsRedirectBetweenWhitelistedHosts(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Hostname() == "howlongtobeat.com" {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://www.howlongtobeat.com/api/bleed/init"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    request,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("{}")),
			Request:    request,
		}, nil
	})}
	request, err := http.NewRequest(http.MethodGet, "https://howlongtobeat.com/api/bleed/init", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, err := doTrustedRequest(client, request, "howlongtobeat.com", "www.howlongtobeat.com")
	if err != nil {
		t.Fatalf("trusted request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || requests != 2 {
		t.Fatalf("status=%d requests=%d", response.StatusCode, requests)
	}
}
