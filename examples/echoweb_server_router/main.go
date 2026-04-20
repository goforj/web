package main

import (
	"fmt"
	"github.com/goforj/web/adapter/echoweb"
)

func main() {
	server, _ := echoweb.NewServer(echoweb.ServerConfig{})
	fmt.Println(server.Router() != nil)
	// true
}
