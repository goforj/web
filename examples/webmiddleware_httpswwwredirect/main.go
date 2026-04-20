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
	req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
	ctx := webtest.NewContext(req, nil, "/docs", nil)
	_ = webmiddleware.HTTPSWWWRedirect()(func(c web.Context) error { return nil })(ctx)
	fmt.Println(ctx.Response().Header().Get("Location"))
	// https://www.example.com/docs
}
