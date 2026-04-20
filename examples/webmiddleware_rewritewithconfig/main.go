package main

import (
	"fmt"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	fmt.Println(webmiddleware.RewriteWithConfig(webmiddleware.RewriteConfig{
		Rules: map[string]string{"/old/*": "/new/$1"},
	}) != nil)
	// true
}
