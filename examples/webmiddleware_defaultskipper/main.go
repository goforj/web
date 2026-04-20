package main

import (
	"fmt"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	fmt.Println(webmiddleware.DefaultSkipper(nil))
	// false
}
