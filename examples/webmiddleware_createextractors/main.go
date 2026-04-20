package main

import (
	"fmt"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	extractors, err := webmiddleware.CreateExtractors("header:X-API-Key,query:token")
	fmt.Println(err == nil, len(extractors))
	// true 2
}
