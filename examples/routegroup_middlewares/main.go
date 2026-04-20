package main

import (
	"fmt"
	"github.com/goforj/web"
)

func main() {
	group := web.NewRouteGroup("/api", nil, func(next web.Handler) web.Handler { return next })
	fmt.Println(len(group.Middlewares()))
	// 1
}
