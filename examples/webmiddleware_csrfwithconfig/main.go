package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"net/http"
	"net/http/httptest"
	"strings"
)

func main() {
	mw := webmiddleware.CSRFWithConfig(webmiddleware.CSRFConfig{CookieName: "_csrf"})
	ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), nil, "/", nil)
	handler := mw(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
	_ = handler(ctx)
	fmt.Println(strings.Contains(ctx.Response().Header().Get("Set-Cookie"), "_csrf="))
	// true
}
