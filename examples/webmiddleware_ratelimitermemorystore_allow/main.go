package main

import (
	"fmt"
	"github.com/goforj/web/webmiddleware"
	"golang.org/x/time/rate"
	"time"
)

func main() {
	store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
	allowed, err := store.Allow("127.0.0.1")
	fmt.Println(err == nil, allowed)
	// true true
}
