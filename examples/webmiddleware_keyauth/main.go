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
	mw := webmiddleware.KeyAuth(func(key string, c web.Context) (bool, error) {
		return key == "demo-key", nil
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer demo-key")
	ctx := webtest.NewContext(req, nil, "/", nil)
	handler := mw(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
	_ = handler(ctx)
	fmt.Println(ctx.StatusCode())
	// 204
}
