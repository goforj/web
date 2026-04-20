package main

import (
	"fmt"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
)

func main() {
	getter := webmiddleware.MethodFromHeader("X-HTTP-Method-Override")
	ctx := webtest.NewContext(nil, nil, "/", nil)
	ctx.Request().Header.Set("X-HTTP-Method-Override", "PATCH")
	fmt.Println(getter(ctx))
	// PATCH
}
