package main

import (
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.RequestIDWithConfig(webmiddleware.RequestIDConfig{
		TargetHeader: "X-Correlation-ID",
		ContextKey:   "correlation_id",
	}))
}
