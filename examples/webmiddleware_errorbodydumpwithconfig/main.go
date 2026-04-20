package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	mw := webmiddleware.ErrorBodyDumpWithConfig(webmiddleware.ErrorBodyDumpConfig{
		Handler: func(c web.Context, status int, body []byte) {},
	})
	_ = mw
}
