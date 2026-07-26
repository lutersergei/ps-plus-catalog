package psextra

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmbeddedAssetsContainApplicationResources(t *testing.T) {
	assets := EmbeddedAssets()
	if !strings.Contains(assets.IndexHTML, `{{define "cards"}}`) {
		t.Fatal("встроенный HTML не содержит шаблон карточек")
	}
	if !json.Valid(assets.CatalogDateBackfill) {
		t.Fatal("встроенный исторический манифест не является корректным JSON")
	}
	assets.CatalogDateBackfill[0] = 'x'
	if !json.Valid(EmbeddedAssets().CatalogDateBackfill) {
		t.Fatal("вызывающий код не должен изменять встроенный манифест")
	}
}
