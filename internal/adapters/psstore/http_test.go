package psstore

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFetchLanguagesRejectsUntrustedURLWithoutRequest(t *testing.T) {
	requests := 0
	client := NewClient(&http.Client{Transport: roundTripFunction(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})})
	_, _, err := client.FetchLanguages(context.Background(), "https://store.playstation.com.evil.example/concept/1")
	if err == nil {
		t.Fatal("недоверенный URL должен быть отклонён")
	}
	if requests != 0 {
		t.Fatalf("выполнено запросов: %d", requests)
	}
}

func TestTrustedRequestRejectsCrossHostRedirect(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunction(func(request *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://evil.example/internal"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})}
	request, err := http.NewRequest(http.MethodGet, "https://store.playstation.com/concept/1", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, err := doTrustedRequest(client, request, "store.playstation.com")
	if response != nil {
		response.Body.Close()
	}
	if err == nil || requests != 1 {
		t.Fatalf("response=%v err=%v requests=%d", response, err, requests)
	}
}

type roundTripFunction func(*http.Request) (*http.Response, error)

func (function roundTripFunction) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
