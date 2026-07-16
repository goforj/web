package main

import (
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
	"net/url"
)

func main() {
	target, _ := url.Parse("http://localhost:8080")
	balancer := webmiddleware.NewRoundRobinBalancer([]*webmiddleware.ProxyTarget{{URL: target}})

	router := echoweb.New().Router()

	router.Use(webmiddleware.ProxyWithConfig(webmiddleware.ProxyConfig{
		Balancer: balancer,
		Rewrite: map[string]string{
			"/api/*": "/$1",
		},
	}))
}
