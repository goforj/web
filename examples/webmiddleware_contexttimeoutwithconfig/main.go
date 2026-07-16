package main

import (
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
	"time"
)

func main() {
	router := echoweb.New().Router()

	router.Use(webmiddleware.ContextTimeoutWithConfig(webmiddleware.ContextTimeoutConfig{
		Timeout: time.Second,
	}))
}
