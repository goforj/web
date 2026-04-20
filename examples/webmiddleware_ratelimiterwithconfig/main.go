package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"golang.org/x/time/rate"
	"net/http"
	"time"
)

func main() {
	store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
	mw := webmiddleware.RateLimiterWithConfig(webmiddleware.RateLimiterConfig{Store: store})
	ctx := webtest.NewContext(nil, nil, "/", nil)
	handler := mw(func(c web.Context) error { return c.NoContent(http.StatusAccepted) })
	_ = handler(ctx)
	fmt.Println(ctx.StatusCode())
	// 202
}
