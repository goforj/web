package main

import (
	"fmt"
	"github.com/goforj/web"
	"net/http"
)

func main() {
	entries := web.BuildRouteEntries([]web.RouteGroup{
		web.NewRouteGroup("/api", []web.Route{
			web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }),
		}),
	})
	fmt.Println(entries[0].Path, entries[0].Methods[0])
	// /api/healthz GET
}
