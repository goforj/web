package main

import (
	"github.com/goforj/web/webmiddleware"
	"golang.org/x/time/rate"
	"time"
)

func main() {
	store := webmiddleware.NewRateLimiterMemoryStoreWithConfig(webmiddleware.RateLimiterMemoryStoreConfig{Rate: rate.Every(time.Second)})
	_ = store
}
