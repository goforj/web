package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()
	router.Use(webmiddleware.Secure())

	router.GET("/", func(c web.Context) error {
		return c.Text(200, "home")
	})
}
