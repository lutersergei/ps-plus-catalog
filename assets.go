// Package psextra предоставляет исполняемой точке встроенные ресурсы проекта.
// Файл находится в корне модуля, потому что go:embed не разрешает обращаться к
// родительским каталогам из cmd/ps-extra.
package psextra

import _ "embed"

// Assets содержит неизменяемые данные, которые должны попасть внутрь бинарника.
type Assets struct {
	IndexHTML           string
	CatalogDateBackfill []byte
}

//go:embed templates/index.html
var indexHTML string

//go:embed catalog_dates_backfill.json
var catalogDateBackfillJSON []byte

// EmbeddedAssets возвращает встроенный HTML и исторический манифест дат.
func EmbeddedAssets() Assets {
	return Assets{
		IndexHTML:           indexHTML,
		CatalogDateBackfill: append([]byte(nil), catalogDateBackfillJSON...),
	}
}
