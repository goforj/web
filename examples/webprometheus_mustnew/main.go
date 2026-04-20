package main

import (
	"github.com/goforj/web/webprometheus"
)

func main() {
	_ = webprometheus.MustNew(webprometheus.Config{})
	// true
}
