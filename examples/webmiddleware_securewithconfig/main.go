package main

import (
	"github.com/goforj/web/webmiddleware"
)

func main() {
	_ = webmiddleware.SecureWithConfig(webmiddleware.SecureConfig{ReferrerPolicy: "same-origin"})
	// true
}
