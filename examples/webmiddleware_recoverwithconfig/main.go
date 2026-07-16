package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.RecoverWithConfig(webmiddleware.RecoverConfig{
		DisableStack: true,
		HandleError: func(c web.Context, err error, stack []byte) error {
			return c.JSON(500, map[string]any{"error": "internal server error"})
		},
	}))
}
