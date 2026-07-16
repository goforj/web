package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
	"log"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.RequestLoggerWithConfig(webmiddleware.RequestLoggerConfig{
		LogValuesFunc: func(c web.Context, values webmiddleware.RequestLoggerValues) error {
			log.Printf("%s %s %d %s", values.Method, values.URI, values.Status, values.Latency)
			return nil
		},
	}))

	router.GET("/users/:id", func(c web.Context) error {
		return c.NoContent(204)
	})
}
