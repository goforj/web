package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	mw := webmiddleware.BasicAuthWithConfig(webmiddleware.BasicAuthConfig{
		Realm: "Example",
		Validator: func(user, pass string, c web.Context) (bool, error) { return true, nil },
	})
	_ = mw
}
