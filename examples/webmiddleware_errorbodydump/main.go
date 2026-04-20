package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	mw := webmiddleware.ErrorBodyDump(func(c web.Context, status int, body []byte) {})
	_ = mw
}
