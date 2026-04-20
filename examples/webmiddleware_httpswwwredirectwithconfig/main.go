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
	req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
	ctx := webtest.NewContext(req, nil, "/docs", nil)
	_ = webmiddleware.HTTPSWWWRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})(func(c web.Context) error { return nil })(ctx)
	fmt.Println(ctx.StatusCode())
	// 307
}
