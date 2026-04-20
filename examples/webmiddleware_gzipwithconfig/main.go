package main

import (
	"github.com/goforj/web/webmiddleware"
)

func main() {
	_ = webmiddleware.GzipWithConfig(webmiddleware.GzipConfig{MinLength: 256})
	// true
}
