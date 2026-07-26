package domain

import "time"

// AnnouncementRef описывает официальный анонс из sitemap PlayStation Blog и
// версию, используемую для кэширования.
type AnnouncementRef struct {
	URL          string
	LastModified string
}

// CatalogAddition содержит игру из анонса и дату её доступности.
type CatalogAddition struct {
	Title     string
	AddedOn   time.Time
	SourceURL string
}

// CatalogAnnouncement содержит разобранный официальный анонс каталога.
type CatalogAnnouncement struct {
	URL          string
	LastModified string
	PublishedOn  time.Time
	Games        []CatalogAddition
}

// AnnouncementCacheVersion — сохранённая версия разобранного анонса.
type AnnouncementCacheVersion struct {
	LastModified  string
	ParserVersion int
}

// CachedAnnouncement — представление анонса для сохранения в кэше.
type CachedAnnouncement struct {
	CatalogAnnouncement
	ParserVersion int
}

// CatalogDateCandidate — пара названия и даты из кэша анонсов.
type CatalogDateCandidate struct {
	Title     string
	AddedOn   time.Time
	SourceURL string
}

// CatalogDateTarget — текущий открытый период присутствия, дату которого можно
// уточнить по официальному или вручную проверенному источнику.
type CatalogDateTarget struct {
	MembershipID      int64
	GameID            string
	Title             string
	TitleEn           string
	FirstSeenAt       time.Time
	AddedOn           *time.Time
	AddedOnSource     string
	PreviousRemovedOn *time.Time
	Initial           bool
}

// CatalogDateMatch связывает официальный анонс с периодом присутствия.
type CatalogDateMatch struct {
	MembershipID int64
	AddedOn      time.Time
	SourceURL    string
}

// CatalogDateBackfillMatch связывает проверенный исторический источник с периодом.
type CatalogDateBackfillMatch struct {
	MembershipID int64
	AddedOn      time.Time
	SourceURL    string
}

// CatalogDatePlan рассчитывается сервисом дат и атомарно применяется адаптером
// хранения.
type CatalogDatePlan struct {
	BackfillMatches     []CatalogDateBackfillMatch
	KeepNullIDs         []int64
	AnnouncementMatches []CatalogDateMatch
	ResetMembershipIDs  []int64
}

// CatalogDateApplyResult описывает объём транзакции и число изменённых строк.
type CatalogDateApplyResult struct {
	Candidates int
	Targets    int
	Updated    int64
}
