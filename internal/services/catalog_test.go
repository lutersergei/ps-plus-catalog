package services

import (
	"context"
	"testing"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

type catalogReaderStub struct {
	listCalls    int
	yearsCalls   int
	genresCalls  int
	bucketsCalls int
}

func (reader *catalogReaderStub) ListGames(context.Context, domain.ListParams) (domain.ListResult, error) {
	reader.listCalls++
	return domain.ListResult{Total: 1, Page: 1, PageSize: 25, TotalPages: 1}, nil
}

func (reader *catalogReaderStub) DistinctYears(context.Context) ([]int, error) {
	reader.yearsCalls++
	return []int{2026}, nil
}

func (reader *catalogReaderStub) DistinctGenres(context.Context) ([]string, error) {
	reader.genresCalls++
	return []string{"Action"}, nil
}

func (reader *catalogReaderStub) IndexBuckets(context.Context, domain.ListParams) ([]domain.IndexBucket, error) {
	reader.bucketsCalls++
	return []domain.IndexBucket{{Label: "A"}}, nil
}

func TestCatalogServiceFragmentSkipsPageMetadata(t *testing.T) {
	reader := &catalogReaderStub{}
	result, err := NewCatalogService(reader).Browse(context.Background(), domain.ListParams{}, false)
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if result.Result.Total != 1 || reader.listCalls != 1 {
		t.Fatalf("result=%+v calls=%+v", result, reader)
	}
	if reader.yearsCalls != 0 || reader.genresCalls != 0 || reader.bucketsCalls != 0 {
		t.Fatalf("фрагмент запросил метаданные полной страницы: %+v", reader)
	}
}

func TestCatalogServiceFullPageLoadsMetadata(t *testing.T) {
	reader := &catalogReaderStub{}
	result, err := NewCatalogService(reader).Browse(context.Background(), domain.ListParams{}, true)
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if len(result.Years) != 1 || len(result.Genres) != 1 || len(result.Buckets) != 1 {
		t.Fatalf("metadata=%+v", result)
	}
	if reader.yearsCalls != 1 || reader.genresCalls != 1 || reader.bucketsCalls != 1 {
		t.Fatalf("calls=%+v", reader)
	}
}
