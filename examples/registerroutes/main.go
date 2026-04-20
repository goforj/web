package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"net/http"
)

func main() {
	adapter := echoweb.New()
	groups := []web.RouteGroup{
		web.NewRouteGroup("/api", []web.Route{
			web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }),
		}),
	}
	err := web.RegisterRoutes(adapter.Router(), groups)
	fmt.Println(err == nil)
	// true
}
