package main

import (
	"github.com/goforj/web/adapter/echoweb"
)

func main() {
	adapter := echoweb.Wrap(nil)
	_ = adapter.Echo()
	// true
}
