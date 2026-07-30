package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
	"log"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.BodyDumpWithConfig(webmiddleware.BodyDumpConfig{
		Skipper: func(c web.Context) bool {
			return c.Path() == "/healthz"
		},
		Handler: func(c web.Context, reqBody, resBody []byte) {
			log.Printf("%s %s -> %d bytes", c.Method(), c.URI(), len(resBody))
			// POST /webhooks -> 16 bytes
		},
	}))
}
