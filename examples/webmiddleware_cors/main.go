package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()
	router.Use(webmiddleware.CORS())

	router.GET("/api/healthz", func(c web.Context) error {
		return c.JSON(200, map[string]any{"ok": true})
	})
}
