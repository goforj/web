package main

import (
	"github.com/goforj/web/webmiddleware"
	"net/url"
)

func main() {
	target, _ := url.Parse("http://localhost:8080")
	mw := webmiddleware.ProxyWithConfig(webmiddleware.ProxyConfig{
		Balancer: webmiddleware.NewRandomBalancer([]*webmiddleware.ProxyTarget{{URL: target}}),
	})
	_ = mw
}
