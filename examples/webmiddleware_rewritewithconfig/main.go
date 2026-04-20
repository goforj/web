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
	req := httptest.NewRequest(http.MethodGet, "/old/users", nil)
	ctx := webtest.NewContext(req, nil, "/old/*", nil)
	handler := webmiddleware.RewriteWithConfig(webmiddleware.RewriteConfig{
		Rules: map[string]string{"/old/*": "/v2/$1"},
	})(func(c web.Context) error {
		fmt.Println(c.Request().URL.Path)
		return nil
	})
	_ = handler(ctx)
	// /v2/users
}
