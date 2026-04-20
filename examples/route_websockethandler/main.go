package main

import (
	"github.com/goforj/web"
)

func main() {
	route := web.NewWebSocketRoute("/ws", func(c web.Context, conn web.WebSocketConn) error { return nil })
	_ = route.WebSocketHandler()
	// true
}
