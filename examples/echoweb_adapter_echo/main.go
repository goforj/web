package main

import (
	"fmt"
	"github.com/goforj/web/adapter/echoweb"
)

func main() {
	adapter := echoweb.New()
	fmt.Println(adapter.Echo() != nil)
	// true
}
