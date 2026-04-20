package main

import (
	"fmt"
	"github.com/goforj/web"
	"net/http"
)

func main() {
	route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error {
		return c.NoContent(http.StatusOK)
	})
	fmt.Println(route.Method(), route.Path())
	// GET /healthz
}
