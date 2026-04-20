package main

import (
	"fmt"
	"github.com/goforj/web/webmiddleware"
	"net/url"
)

func main() {
	target, _ := url.Parse("http://localhost:8080")
	balancer := webmiddleware.NewRoundRobinBalancer([]*webmiddleware.ProxyTarget{{URL: target}})
	fmt.Println(balancer.Next(nil).URL.Host)
	// localhost:8080
}
