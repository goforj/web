package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"net/http"
)

func main() {
	ctx := webtest.NewContext(nil, nil, "/", nil)
	handler := webmiddleware.Secure()(func(c web.Context) error { return c.NoContent(http.StatusOK) })
	_ = handler(ctx)
	fmt.Println(ctx.Response().Header().Get("X-Frame-Options"))
	// SAMEORIGIN
}
