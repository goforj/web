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
	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	ctx := webtest.NewContext(req, nil, "/docs", nil)
	handler := webmiddleware.AddTrailingSlash()(func(c web.Context) error {
		fmt.Println(c.Request().URL.Path)
		return nil
	})
	_ = handler(ctx)
	// /docs/
}
