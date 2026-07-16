package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
	"golang.org/x/time/rate"
	"time"
)

func main() {
	store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))

	router := echoweb.New().Router()
	router.Use(webmiddleware.RateLimiter(store))

	router.POST("/api/messages", func(c web.Context) error {
		return c.NoContent(202)
	})
}
