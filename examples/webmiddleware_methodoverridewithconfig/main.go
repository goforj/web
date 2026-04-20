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
	req := httptest.NewRequest(http.MethodPost, "/?_method=DELETE", nil)
	ctx := webtest.NewContext(req, nil, "/", nil)
	handler := webmiddleware.MethodOverrideWithConfig(webmiddleware.MethodOverrideConfig{
		Getter: webmiddleware.MethodFromQuery("_method"),
	})(func(c web.Context) error {
		fmt.Println(c.Method())
		return nil
	})
	_ = handler(ctx)
	// DELETE
}
