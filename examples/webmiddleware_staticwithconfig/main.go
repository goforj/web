package main

import (
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.StaticWithConfig(webmiddleware.StaticConfig{
		Root:  "public",
		HTML5: true,
	}))
}
