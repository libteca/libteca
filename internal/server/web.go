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
		return http.FS(emptyFS{})
	}
	return http.FS(sub)
}

type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}
