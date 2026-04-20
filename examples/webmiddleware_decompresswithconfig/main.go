package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
)

func main() {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("plain"))
	ctx := webtest.NewContext(req, nil, "/", nil)
	handler := webmiddleware.DecompressWithConfig(webmiddleware.DecompressConfig{})(func(c web.Context) error {
		data, _ := io.ReadAll(c.Request().Body)
		fmt.Println(string(data))
		return nil
	})
	_ = handler(ctx)
	// plain
}
