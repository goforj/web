package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.KeyAuthWithConfig(webmiddleware.KeyAuthConfig{
		KeyLookup: "query:api_key",
		Validator: func(key string, c web.Context) (bool, error) {
			return key == "demo-key", nil
		},
	}))

	router.GET("/api/reports", func(c web.Context) error {
		return c.JSON(200, map[string]any{"ready": true})
	})
}
