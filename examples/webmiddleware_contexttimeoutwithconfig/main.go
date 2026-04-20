package main

import (
	"github.com/goforj/web/webmiddleware"
	"time"
)

func main() {
	_ = webmiddleware.ContextTimeoutWithConfig(webmiddleware.ContextTimeoutConfig{Timeout: time.Second})
	// true
}
