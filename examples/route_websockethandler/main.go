package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webtest"
)

func main() {
	route := web.NewWebSocketRoute("/ws", func(c web.Context, conn web.WebSocketConn) error {
		c.Set("ready", true)
		return nil
	})
	ctx := webtest.NewContext(nil, nil, "/ws", nil)
	err := route.WebSocketHandler()(ctx, nil)
	fmt.Println(err == nil, ctx.Get("ready"))
	// true true
}
