package main

import (
	"fmt"
	"github.com/goforj/web/webmiddleware"
	"golang.org/x/time/rate"
	"time"
)

func main() {
	store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
	allowed1, _ := store.Allow("192.0.2.1")
	allowed2, _ := store.Allow("192.0.2.1")
	fmt.Println(allowed1, allowed2)
	// true false
}
