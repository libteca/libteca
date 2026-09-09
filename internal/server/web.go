package server

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:webdist
var webEmbedded embed.FS

func webFS() http.FileSystem {
	sub, err := fs.Sub(webEmbedded, "webdist")
	if err != nil {
		return http.Dir(".")
	}
	return http.FS(sub)
}
