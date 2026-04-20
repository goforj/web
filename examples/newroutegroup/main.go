package main

import (
	"fmt"
	"github.com/goforj/web"
	"net/http"
)

func main() {
	group := web.NewRouteGroup("/api", []web.Route{
		web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }),
	})
	fmt.Println(group.RoutePrefix(), len(group.Routes()))
	// /api 1
}
