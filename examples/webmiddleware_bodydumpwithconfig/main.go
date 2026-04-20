package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	mw := webmiddleware.BodyDumpWithConfig(webmiddleware.BodyDumpConfig{
		Handler: func(c web.Context, reqBody, resBody []byte) {},
	})
	_ = mw
}
