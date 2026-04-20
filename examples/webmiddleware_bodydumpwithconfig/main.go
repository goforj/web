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
	mw := webmiddleware.BodyDumpWithConfig(webmiddleware.BodyDumpConfig{
		Handler: func(c web.Context, reqBody, resBody []byte) { fmt.Println(string(resBody)) },
	})
	ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), nil, "/", nil)
	handler := mw(func(c web.Context) error { return c.Text(http.StatusOK, "ok") })
	_ = handler(ctx)
	// ok
}
