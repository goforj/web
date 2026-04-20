package main

import (
	"github.com/goforj/web/webmiddleware"
	"net/url"
)

func main() {
	target, _ := url.Parse("http://localhost:8080")
	balancer := webmiddleware.NewRandomBalancer([]*webmiddleware.ProxyTarget{{URL: target}})
	_ = webmiddleware.Proxy(balancer)
}
