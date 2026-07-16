package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.CORSWithConfig(webmiddleware.CORSConfig{
		AllowOrigins: []string{"https://app.example.com"},
		AllowMethods: []string{"GET", "POST", "PATCH"},
	}))

	router.GET("/api/healthz", func(c web.Context) error {
		return c.JSON(200, map[string]any{"ok": true})
	})
}
