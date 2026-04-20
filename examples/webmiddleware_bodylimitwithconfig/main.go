package main

import (
	"github.com/goforj/web/webmiddleware"
)

func main() {
	_ = webmiddleware.BodyLimitWithConfig(webmiddleware.BodyLimitConfig{Limit: "2KB"})
}
