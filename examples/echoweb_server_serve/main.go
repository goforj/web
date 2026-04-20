package main

import (
	"context"
	"fmt"
	"github.com/goforj/web/adapter/echoweb"
)

func main() {
	server, _ := echoweb.NewServer(echoweb.ServerConfig{Addr: "127.0.0.1:0"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fmt.Println(server.Serve(ctx) == nil)
	// true
}
