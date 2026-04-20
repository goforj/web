package main

import (
	"github.com/goforj/web/webmiddleware"
	"golang.org/x/time/rate"
	"time"
)

func main() {
	store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
	_ = webmiddleware.RateLimiter(store)
	// true
}
