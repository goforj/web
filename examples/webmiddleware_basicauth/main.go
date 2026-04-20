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
	mw := webmiddleware.BasicAuth(func(user, pass string, c web.Context) (bool, error) {
		return user == "demo" && pass == "secret", nil
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "basic ZGVtbzpzZWNyZXQ=")
	ctx := webtest.NewContext(req, nil, "/", nil)
	handler := mw(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
	_ = handler(ctx)
	fmt.Println(ctx.StatusCode())
	// 204
}
