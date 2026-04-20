package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"net/http"
)

func main() {
	mw := webmiddleware.RequestID()
	handler := mw(func(c web.Context) error {
		_ = c.Get("request_id")
		return c.NoContent(http.StatusOK)
	})
	ctx := webtest.NewContext(nil, nil, "/", nil)
	_ = handler(ctx)
	fmt.Println(ctx.Response().Header().Get("X-Request-ID") != "")
	// true
	// true
}
