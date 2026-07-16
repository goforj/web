package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()
	router.Use(webmiddleware.Timeout())

	router.GET("/healthz", func(c web.Context) error {
		return c.NoContent(204)
	})
}
