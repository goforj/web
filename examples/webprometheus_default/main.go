package main

import (
	"fmt"
	"github.com/goforj/web/webprometheus"
)

func main() {
	fmt.Println(webprometheus.Default() == webprometheus.Default())
	// true
}
