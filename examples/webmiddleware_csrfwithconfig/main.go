package main

import (
	"github.com/goforj/web/webmiddleware"
)

func main() {
	mw := webmiddleware.CSRFWithConfig(webmiddleware.CSRFConfig{CookieName: "_csrf"})
	_ = mw
}
