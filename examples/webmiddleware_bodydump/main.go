package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
	"log"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.BodyDump(func(c web.Context, reqBody, resBody []byte) {
		log.Printf("%s %s -> %d bytes", c.Method(), c.URI(), len(resBody))
	}))

	router.POST("/webhooks", func(c web.Context) error {
		return c.JSON(202, map[string]any{"queued": true})
	})
}
