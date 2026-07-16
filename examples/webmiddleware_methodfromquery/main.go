package main

import (
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()

	router.Pre(webmiddleware.MethodOverrideWithConfig(webmiddleware.MethodOverrideConfig{
		Getter: webmiddleware.MethodFromQuery("_method"),
	}))
}
