package main

import (
	"fmt"
	"github.com/goforj/web"
)

func main() {
	group := web.NewRouteGroup("/api", nil)
	fmt.Println(group.RoutePrefix())
	// /api
}
