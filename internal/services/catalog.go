// Package services содержит прикладные сценарии каталога и синхронизации.
// Сервисы зависят только от небольших контрактов, принадлежащих потребителю.
package services

import (
	"context"
	"fmt"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

// CatalogReader описывает операции чтения, необходимые сценарию просмотра.
type CatalogReader interface {
	ListGames(context.Context, domain.ListParams) (domain.ListResult, error)
	DistinctYears(context.Context) ([]int, error)
	DistinctGenres(context.Context) ([]string, error)
	IndexBuckets(context.Context, domain.ListParams) ([]domain.IndexBucket, error)
}

// CatalogService координирует чтение каталога, не зная способа хранения.
type CatalogService struct {
	reader CatalogReader
}

// NewCatalogService создаёт сервис просмотра каталога.
func NewCatalogService(reader CatalogReader) *CatalogService {
	return &CatalogService{reader: reader}
}

// Browse возвращает страницу игр и, при необходимости, данные полной страницы.
func (s *CatalogService) Browse(
	ctx context.Context,
	params domain.ListParams,
	includeMetadata bool,
) (domain.BrowseResult, error) {
	params.Normalize()
	var browse domain.BrowseResult
	result, err := s.reader.ListGames(ctx, params)
	if err != nil {
		return browse, fmt.Errorf("получить список игр: %w", err)
	}
	browse.Result = result
	if !includeMetadata {
		return browse, nil
	}
	browse.Years, err = s.reader.DistinctYears(ctx)
	if err != nil {
		return domain.BrowseResult{}, fmt.Errorf("получить список годов: %w", err)
	}
	browse.Genres, err = s.reader.DistinctGenres(ctx)
	if err != nil {
		return domain.BrowseResult{}, fmt.Errorf("получить список жанров: %w", err)
	}
	browse.Buckets, err = s.reader.IndexBuckets(ctx, params)
	if err != nil {
		return domain.BrowseResult{}, fmt.Errorf("построить индекс каталога: %w", err)
	}
	return browse, nil
}
