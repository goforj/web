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
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	ctx := webtest.NewContext(req, nil, "/", nil)
	handler := webmiddleware.Compress()(func(c web.Context) error {
		return c.Text(http.StatusOK, "hello")
	})
	_ = handler(ctx)
	fmt.Println(ctx.Response().Header().Get("Content-Encoding"))
	// gzip
}
