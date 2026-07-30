package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
	"log"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.ErrorBodyDumpWithConfig(webmiddleware.ErrorBodyDumpConfig{
		Skipper: func(c web.Context) bool {
			return c.Path() == "/healthz"
		},
		Handler: func(c web.Context, status int, body []byte) {
			log.Printf("%s %s failed with %d", c.Method(), c.URI(), status)
			// GET /reports/42 failed with 404
		},
	}))
}
