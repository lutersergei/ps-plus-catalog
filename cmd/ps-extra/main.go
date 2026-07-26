package main

import (
	"os"

	psextra "github.com/lutersergei/ps-plus-catalog"
	"github.com/lutersergei/ps-plus-catalog/internal/app"
)

func main() {
	assets := psextra.EmbeddedAssets()
	os.Exit(app.Run(os.Args[1:], app.Assets{
		IndexHTML:           assets.IndexHTML,
		CatalogDateBackfill: assets.CatalogDateBackfill,
	}, os.Stdout, os.Stderr))
}
