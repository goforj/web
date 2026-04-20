package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"net/http"
	"net/http/httptest"
)

func main() {
	server, _ := echoweb.NewServer(echoweb.ServerConfig{
		RouteGroups: []web.RouteGroup{
			web.NewRouteGroup("/api", []web.Route{
				web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return c.NoContent(http.StatusOK) }),
			}),
		},
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	server.ServeHTTP(rr, req)
	fmt.Println(rr.Code)
	// 204
}
