package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
)

func main() {
	ctx := webtest.NewContext(nil, nil, "/", nil)
	handler := webmiddleware.RecoverWithConfig(webmiddleware.RecoverConfig{DisableErrorHandler: true})(func(c web.Context) error {
		panic("boom")
	})
	fmt.Println(handler(ctx) != nil)
	// true
}
