package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	mw := webmiddleware.BasicAuth(func(user, pass string, c web.Context) (bool, error) {
		return user == "demo" && pass == "secret", nil
	})
	_ = mw
}
