package main

import (
	"fmt"
	"github.com/goforj/web/webmiddleware"
	"golang.org/x/time/rate"
	"time"
)

func main() {
	store := webmiddleware.NewRateLimiterMemoryStoreWithConfig(webmiddleware.RateLimiterMemoryStoreConfig{Rate: rate.Every(time.Second)})
	allowed, _ := store.Allow("192.0.2.1")
	fmt.Println(allowed)
	// true
}
