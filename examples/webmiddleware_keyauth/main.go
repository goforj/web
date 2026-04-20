package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	mw := webmiddleware.KeyAuth(func(key string, c web.Context) (bool, error) {
		return key == "demo-key", nil
	})
	_ = mw
}
