package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	mw := webmiddleware.KeyAuthWithConfig(webmiddleware.KeyAuthConfig{
		Validator: func(key string, c web.Context) (bool, error) { return true, nil },
	})
	_ = mw
	// true
}
