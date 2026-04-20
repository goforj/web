package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webtest"
	"net/http"
)

func main() {
	route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error {
		return c.NoContent(http.StatusCreated)
	})
	ctx := webtest.NewContext(nil, nil, "/healthz", nil)
	_ = route.Handler()(ctx)
	fmt.Println(ctx.StatusCode())
	// 201
}
