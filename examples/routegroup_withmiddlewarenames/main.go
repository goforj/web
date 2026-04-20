package main

import (
	"fmt"
	"github.com/goforj/web"
)

func main() {
	group := web.NewRouteGroup("/api", nil).WithMiddlewareNames("auth", "trace")
	fmt.Println(len(group.MiddlewareNames()))
	// 2
}
