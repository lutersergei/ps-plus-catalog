// Package domain содержит модель данных приложения, общую для сервисов и
// адаптеров. Пакет намеренно не зависит от реализаций SQL, HTTP и файловой
// системы.
package domain

import "errors"

// ErrProviderQuotaExhausted означает, что квота каждого настроенного ключа
// внешнего провайдера исчерпана на текущий период.
var ErrProviderQuotaExhausted = errors.New("provider quota exhausted")

// CatalogGame описывает один продукт в текущем каталоге PlayStation Plus.
type CatalogGame struct {
	ID          string
	Title       string
	TitleEn     string
	ReleaseYear int
	Genres      []string
	Platforms   []string
	ImageURL    string
	StoreURL    string
}

// CatalogSnapshotResult описывает изменения состава каталога после применения
// полного снимка внешнего источника.
type CatalogSnapshotResult struct {
	Initial     bool
	Added       int64
	Removed     int64
	Deactivated int64
}

// SourceGenre — жанр из внешнего источника. SourceID необязателен, поскольку
// не каждый провайдер предоставляет стабильный идентификатор жанра.
type SourceGenre struct {
	Name     string
	SourceID *int64
}

// ScoreTarget — игра, для которой оценки отсутствуют или устарели.
type ScoreTarget struct {
	ID                         string
	Title                      string
	TitleEn                    string
	NeedsMetacriticURLBackfill bool
}

// LanguageTarget — игра, для которой отсутствуют или устарели сведения о
// языках из PlayStation Store.
type LanguageTarget struct {
	ID         string
	ConceptURL string
}

// Rating содержит оценку по шкале 0–100 и необязательное число голосов.
type Rating struct {
	Score int
	Count int
	Found bool
}

// MetacriticResult содержит полный результат одного запроса к Metacritic.
// Ошибка UserErr не мешает сохранить оценку критиков.
type MetacriticResult struct {
	Critic  Rating
	User    Rating
	Genres  []string
	PageURL string
	UserErr error
}

// OpenCriticGenre — жанр, полученный от OpenCritic.
type OpenCriticGenre struct {
	ID   int
	Name string
}

// OpenCriticResult содержит полный результат одного запроса к OpenCritic.
// Ошибка PlayerErr не мешает сохранить оценку критиков.
type OpenCriticResult struct {
	ID        int
	Critic    Rating
	Player    Rating
	Genres    []OpenCriticGenre
	PageURL   string
	PlayerErr error
}

// HLTBResult содержит используемые поля HowLongToBeat для одной игры.
type HLTBResult struct {
	MainExtraSeconds int
	Rating           int
	GameID           int
	PageURL          string
}

// MetacriticUpdate описывает результат запроса для сохранения. Значение nil
// означает, что провайдер достоверно не вернул соответствующие данные.
type MetacriticUpdate struct {
	Critic    *int64
	User      *int64
	UserCount *int64
	PageURL   *string
}

// OpenCriticUpdate описывает результат запроса OpenCritic для сохранения.
type OpenCriticUpdate struct {
	Critic      *int64
	PageURL     *string
	ID          *int64
	Player      *int64
	PlayerCount *int64
}

// HLTBUpdate описывает достоверный результат запроса HLTB для сохранения.
type HLTBUpdate struct {
	MainExtraSeconds *int64
	Rating           *int64
	ID               *int64
	PageURL          *string
}

// ResetMissingResult сообщает, сколько отметок проверки было сброшено.
type ResetMissingResult struct {
	Metacritic int64
	OpenCritic int64
}
