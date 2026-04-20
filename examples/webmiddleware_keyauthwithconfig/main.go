package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"net/http"
)

func main() {
	mw := webmiddleware.KeyAuthWithConfig(webmiddleware.KeyAuthConfig{
		Validator: func(key string, c web.Context) (bool, error) { return true, nil },
	})
	ctx := webtest.NewContext(nil, nil, "/", nil)
	handler := mw(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
	_ = handler(ctx)
	fmt.Println(ctx.StatusCode())
	// 400
}
