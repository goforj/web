package main

import (
	"fmt"
	"github.com/goforj/web"
)

func main() {
	group := web.NewRouteGroup("/api", nil).WithMiddlewareNames("auth")
	fmt.Println(group.MiddlewareNames()[0])
	// auth
}
