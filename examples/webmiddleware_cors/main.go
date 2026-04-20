package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"net/http"
	"net/http/httptest"
)

func main() {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://example.com")
	ctx := webtest.NewContext(req, nil, "/", nil)
	handler := webmiddleware.CORS()(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
	_ = handler(ctx)
	fmt.Println(ctx.Response().Header().Get("Access-Control-Allow-Origin"))
	// *
}
