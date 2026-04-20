package main

import (
	"fmt"
	"github.com/goforj/web/adapter/echoweb"
)

func main() {
	_, ok := echoweb.UnwrapWebSocketConn(nil)
	fmt.Println(ok)
	// false
}
