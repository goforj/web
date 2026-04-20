package main

import (
	"github.com/goforj/web/adapter/echoweb"
)

func main() {
	adapter := echoweb.New()
	_ = adapter.Echo()
	// true
}
