package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	mw := webmiddleware.BodyDump(func(c web.Context, reqBody, resBody []byte) {})
	_ = mw
}
