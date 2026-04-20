package main

import (
	"fmt"
	"github.com/goforj/web/webprometheus"
)

func main() {
	metrics, err := webprometheus.New(webprometheus.Config{Namespace: "app"})
	fmt.Println(err == nil)
	// true true
}
