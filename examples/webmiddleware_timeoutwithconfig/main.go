package main

import (
	"github.com/goforj/web/webmiddleware"
	"time"
)

func main() {
	_ = webmiddleware.TimeoutWithConfig(webmiddleware.TimeoutConfig{Timeout: time.Second})
	// true
}
