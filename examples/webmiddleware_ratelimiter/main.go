package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"golang.org/x/time/rate"
	"net/http"
	"net/http/httptest"
	"time"
)

func main() {
	store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
	handler := webmiddleware.RateLimiter(store)(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "192.0.2.10:1234"
	ctx1 := webtest.NewContext(req1, nil, "/", nil)
	_ = handler(ctx1)
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "192.0.2.10:1234"
	ctx2 := webtest.NewContext(req2, nil, "/", nil)
	_ = handler(ctx2)
	fmt.Println(ctx1.StatusCode(), ctx2.StatusCode())
	// 204 429
}
