package main

import (
	"fmt"
	"github.com/goforj/web"
	"net/http"
)

func main() {
	route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }).WithMiddlewareNames("auth", "trace")
	fmt.Println(len(route.MiddlewareNames()))
	// 2
}
