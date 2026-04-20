package main

import (
	"fmt"
	"github.com/goforj/web/webprometheus"
)

func main() {
	metrics, err := webprometheus.New(webprometheus.Config{Namespace: "app"})
	_ = metrics
	fmt.Println(err == nil)
	// true
}
