package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"net/http"
	"net/http/httptest"
)

func main() {
	adapter := echoweb.New()
	adapter.Router().GET("/healthz", func(c web.Context) error { return c.NoContent(http.StatusOK) })
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	adapter.ServeHTTP(rr, req)
	fmt.Println(rr.Code)
	// 204
}
