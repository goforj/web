package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()
	router.Use(webmiddleware.RequestID())

	router.GET("/healthz", func(c web.Context) error {
		return c.JSON(200, map[string]any{
			"request_id": c.Get("request_id"),
		})
	})
}
