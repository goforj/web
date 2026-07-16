package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()

	router.Pre(webmiddleware.RewriteWithConfig(webmiddleware.RewriteConfig{
		Rules: map[string]string{"/old/*": "/v2/$1"},
	}))

	router.GET("/v2/:name", func(c web.Context) error {
		return c.Text(200, c.Param("name"))
	})
}
