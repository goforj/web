package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()

	router.Pre(webmiddleware.Rewrite(map[string]string{
		"/old/*": "/new/$1",
	}))

	router.GET("/new/:name", func(c web.Context) error {
		return c.Text(200, c.Param("name"))
	})
}
