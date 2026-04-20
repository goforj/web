package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"net/http"
	"net/http/httptest"
	"strings"
)

func main() {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello"))
	ctx := webtest.NewContext(req, nil, "/", nil)
	handler := webmiddleware.BodyLimit("2B")(func(c web.Context) error {
		return c.NoContent(http.StatusOK)
	})
	_ = handler(ctx)
	fmt.Println(ctx.StatusCode())
	// 413
}
