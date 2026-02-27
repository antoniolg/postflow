package api

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets/*
var uiAssets embed.FS

func uiAssetsHandler() http.Handler {
	sub, err := fs.Sub(uiAssets, "assets")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(sub))
}
