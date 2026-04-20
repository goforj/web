package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	mw := webmiddleware.RequestLoggerWithConfig(webmiddleware.RequestLoggerConfig{
		LogValuesFunc: func(c web.Context, values webmiddleware.RequestLoggerValues) error { return nil },
	})
	_ = mw
}
