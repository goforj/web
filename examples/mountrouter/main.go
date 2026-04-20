package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
)

func main() {
	adapter := echoweb.New()
	err := web.MountRouter(adapter.Router(), []web.RouterMount{
		func(r web.Router) error {
			r.GET("/healthz", func(c web.Context) error { return nil })
			return nil
		},
	})
	fmt.Println(err == nil)
	// true
}
