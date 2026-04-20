package main

import (
	"github.com/goforj/web/webmiddleware"
)

func main() {
	mw := webmiddleware.CORSWithConfig(webmiddleware.CORSConfig{AllowOrigins: []string{"https://example.com"}})
	_ = mw
}
