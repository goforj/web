package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()
	router.Pre(webmiddleware.AddTrailingSlash())

	router.GET("/docs/", func(c web.Context) error {
		return c.Text(200, "docs")
	})
}
