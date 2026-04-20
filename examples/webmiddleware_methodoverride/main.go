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
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-HTTP-Method-Override", http.MethodPatch)
	ctx := webtest.NewContext(req, nil, "/", nil)
	handler := webmiddleware.MethodOverride()(func(c web.Context) error {
		fmt.Println(c.Method())
		return nil
	})
	_ = handler(ctx)
	// PATCH
}
