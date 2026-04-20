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
	req := httptest.NewRequest(http.MethodGet, "/docs/", nil)
	ctx := webtest.NewContext(req, nil, "/docs/", nil)
	handler := webmiddleware.RemoveTrailingSlashWithConfig(webmiddleware.TrailingSlashConfig{RedirectCode: 308})(func(c web.Context) error {
		return c.NoContent(204)
	})
	_ = handler(ctx)
	fmt.Println(ctx.StatusCode(), ctx.Response().Header().Get("Location"))
	// 308 /docs
}
