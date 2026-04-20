package main

import (
	"fmt"
	"github.com/goforj/web"
)

func main() {
	route := web.NewWebSocketRoute("/ws", func(c web.Context, conn web.WebSocketConn) error {
		return nil
	})
	fmt.Println(route.IsWebSocket())
	// true
}
