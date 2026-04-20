package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"time"
)

func main() {
	ctx := webtest.NewContext(nil, nil, "/", nil)
	handler := webmiddleware.ContextTimeoutWithConfig(webmiddleware.ContextTimeoutConfig{Timeout: time.Second})(func(c web.Context) error {
		fmt.Println(c.Request().Context().Err() == nil)
		return nil
	})
	_ = handler(ctx)
	// true
}
