package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"net/http"
	"net/http/httptest"
)

func main() {
	ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), nil, "/", nil)
	handler := webmiddleware.CSRF()(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
	_ = handler(ctx)
	fmt.Println(ctx.Response().Header().Get("Set-Cookie") != "")
	// true
}
