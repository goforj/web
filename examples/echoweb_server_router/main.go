package main

import (
	"github.com/goforj/web/adapter/echoweb"
)

func main() {
	server, _ := echoweb.NewServer(echoweb.ServerConfig{})
	_ = server.Router()
	// true
}
