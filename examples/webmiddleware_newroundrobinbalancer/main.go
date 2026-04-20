package main

import (
	"github.com/goforj/web/webmiddleware"
	"net/url"
)

func main() {
	target, _ := url.Parse("http://localhost:8080")
	balancer := webmiddleware.NewRoundRobinBalancer([]*webmiddleware.ProxyTarget{{URL: target}})
	_ = balancer
}
