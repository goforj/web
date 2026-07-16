package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
	"time"
)

func main() {
	router := echoweb.New().Router()
	router.Use(webmiddleware.ContextTimeout(2 * time.Second))

	router.GET("/reports", func(c web.Context) error {
		return c.JSON(200, map[string]any{"ready": true})
	})
}
