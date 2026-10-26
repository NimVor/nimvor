package web

import (
	"embed"
	"io/fs"
)

//go:embed all:assets
var assetFiles embed.FS

// Assets returns an http.FS rooted at the "assets/" folder (CSS, JS, fonts,
// vendor libs) ready to be served under /assets/.
func Assets() (fs.FS, error) {
	return fs.Sub(assetFiles, "assets")
}
