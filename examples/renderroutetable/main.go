package main

import (
	"fmt"
	"github.com/goforj/web"
	"strings"
)

func main() {
	table := web.RenderRouteTable([]web.RouteEntry{{
		Path:    "/api/healthz",
		Handler: "monitoring.Healthz",
		Methods: []string{"GET"},
	}})
	fmt.Println(strings.Contains(table, "/api/healthz"))
	// true
}
