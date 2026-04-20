package main

import (
	"github.com/goforj/web/webmiddleware"
)

func main() {
	_ = webmiddleware.Rewrite(map[string]string{"/old/*": "/new/$1"})
}
