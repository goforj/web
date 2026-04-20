package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"net/http"
	"net/http/httptest"
	"net/url"
)

func main() {
	target, _ := url.Parse("http://localhost:8080")
	mw := webmiddleware.ProxyWithConfig(webmiddleware.ProxyConfig{
		Balancer: webmiddleware.NewRandomBalancer([]*webmiddleware.ProxyTarget{{URL: target}}),
	})
	req := httptest.NewRequest(http.MethodGet, "/old/path", nil)
	ctx := webtest.NewContext(req, nil, "/", nil)
	_ = mw(func(c web.Context) error { return nil })(ctx)
	fmt.Println(ctx.Get("target").(*webmiddleware.ProxyTarget).URL.Host)
	// localhost:8080
}
