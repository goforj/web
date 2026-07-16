package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.BodyLimitWithConfig(webmiddleware.BodyLimitConfig{
		Limit: "10MB",
	}))

	router.POST("/imports", func(c web.Context) error {
		return c.NoContent(202)
	})
}
