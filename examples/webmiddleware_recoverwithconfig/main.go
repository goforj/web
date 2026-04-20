package main

import (
	"github.com/goforj/web/webmiddleware"
)

func main() {
	_ = webmiddleware.RecoverWithConfig(webmiddleware.RecoverConfig{})
	// true
}
