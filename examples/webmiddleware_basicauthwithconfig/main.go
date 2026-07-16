package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.BasicAuthWithConfig(webmiddleware.BasicAuthConfig{
		Realm: "Admin",
		Validator: func(user, pass string, c web.Context) (bool, error) {
			return user == "demo" && pass == "secret", nil
		},
	}))

	router.GET("/admin", func(c web.Context) error {
		return c.Text(200, "welcome")
	})
}
