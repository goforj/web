package main

import (
	"github.com/goforj/web/webmiddleware"
	"time"
)

func main() {
	_ = webmiddleware.ContextTimeout(2 * time.Second)
	// true
}
