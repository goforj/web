package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"net/http"
)

func main() {
	server, err := echoweb.NewServer(echoweb.ServerConfig{
		RouteGroups: []web.RouteGroup{
			web.NewRouteGroup("/api", []web.Route{
				web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return c.NoContent(http.StatusOK) }),
			}),
		},
	})
	fmt.Println(err == nil)
	// true true
}
