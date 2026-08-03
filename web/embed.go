// Package web holds the embedded frontend: templates, htmx, mermaid, CSS.
package web

import (
	"embed"
	"io/fs"
)

//go:embed templates
var Templates embed.FS

//go:embed assets
var assets embed.FS

// AssetsFS returns the static assets rooted at assets/.
func AssetsFS() fs.FS {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err)
	}
	return sub
}
