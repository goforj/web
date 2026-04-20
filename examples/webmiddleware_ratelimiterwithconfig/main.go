package main

import (
	"github.com/goforj/web/webmiddleware"
	"golang.org/x/time/rate"
	"time"
)

func main() {
	store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
	mw := webmiddleware.RateLimiterWithConfig(webmiddleware.RateLimiterConfig{Store: store})
	_ = mw
	// true
}
