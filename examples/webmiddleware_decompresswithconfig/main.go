package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.DecompressWithConfig(webmiddleware.DecompressConfig{
		Skipper: func(c web.Context) bool {
			return c.Path() == "/webhooks/raw"
		},
	}))

	router.POST("/ingest", func(c web.Context) error {
		return c.NoContent(202)
	})
}
