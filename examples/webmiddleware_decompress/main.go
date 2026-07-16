package main

import (
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
	"io"
)

func main() {
	router := echoweb.New().Router()
	router.Use(webmiddleware.Decompress())

	router.POST("/ingest", func(c web.Context) error {
		data, _ := io.ReadAll(c.Request().Body)
		return c.JSON(200, map[string]int{"bytes": len(data)})
	})
}
