package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"net/http"
)

func main() {
	mw := webmiddleware.ErrorBodyDumpWithConfig(webmiddleware.ErrorBodyDumpConfig{
		Handler: func(c web.Context, status int, body []byte) { fmt.Println(status) },
	})
	ctx := webtest.NewContext(nil, nil, "/", nil)
	handler := mw(func(c web.Context) error { return c.Text(http.StatusInternalServerError, "boom") })
	_ = handler(ctx)
	// 500
}
