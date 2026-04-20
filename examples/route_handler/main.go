package main

import (
	"github.com/goforj/web"
	"net/http"
)

func main() {
	route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil })
	_ = route.Handler()
	// true
}
