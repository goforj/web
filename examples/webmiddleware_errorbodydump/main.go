package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"net/http"
)

func main() {
	var captured string
	mw := webmiddleware.ErrorBodyDump(func(c web.Context, status int, body []byte) {
		captured = fmt.Sprintf("%d:%s", status, string(body))
	})
	ctx := webtest.NewContext(nil, nil, "/", nil)
	handler := mw(func(c web.Context) error { return c.Text(http.StatusBadRequest, "nope") })
	_ = handler(ctx)
	fmt.Println(captured)
	// 400:nope
}
