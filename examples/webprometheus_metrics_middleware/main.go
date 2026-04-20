package main

import (
	"github.com/goforj/web/webprometheus"
)

func main() {
	metrics, _ := webprometheus.New(webprometheus.Config{})
	_ = metrics.Middleware()
}
