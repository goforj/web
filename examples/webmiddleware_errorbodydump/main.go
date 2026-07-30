package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
	"log"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.ErrorBodyDump(func(c web.Context, status int, body []byte) {
		log.Printf("%s %s failed with %d", c.Method(), c.URI(), status)
		// GET /reports/42 failed with 404
	}))

	router.GET("/reports/:id", func(c web.Context) error {
		return c.Text(404, "report not found")
	})
}
