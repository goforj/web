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
	handler := webmiddleware.Rewrite(map[string]string{"/old/*": "/new/$1"})(func(c web.Context) error {
		fmt.Println(c.Request().URL.Path)
		return nil
	})
	_ = handler(ctx)
	// /new/users
}
