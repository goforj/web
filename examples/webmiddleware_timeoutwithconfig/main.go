package main

import (
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
	"time"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.TimeoutWithConfig(webmiddleware.TimeoutConfig{
		Timeout:      time.Second,
		ErrorMessage: "request timed out",
	}))
}
