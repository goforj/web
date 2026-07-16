package main

import (
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.SecureWithConfig(webmiddleware.SecureConfig{
		ReferrerPolicy:        "same-origin",
		ContentSecurityPolicy: "default-src 'self'",
	}))
}
