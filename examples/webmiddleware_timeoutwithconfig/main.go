package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"net/http"
	"time"
)

func main() {
	ctx := webtest.NewContext(nil, nil, "/", nil)
	handler := webmiddleware.TimeoutWithConfig(webmiddleware.TimeoutConfig{Timeout: time.Second})(func(c web.Context) error {
		return c.NoContent(http.StatusAccepted)
	})
	_ = handler(ctx)
	fmt.Println(ctx.StatusCode())
	// 202
}
