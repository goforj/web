package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"io"
	"net/http"
	"net/http/httptest"
)

func main() {
	var body string
	compressed := &bytes.Buffer{}
	gz := gzip.NewWriter(compressed)
	_, _ = gz.Write([]byte("hello"))
	_ = gz.Close()
	req := httptest.NewRequest(http.MethodPost, "/", compressed)
	req.Header.Set("Content-Encoding", webmiddleware.GZIPEncoding)
	ctx := webtest.NewContext(req, nil, "/", nil)
	handler := webmiddleware.Decompress()(func(c web.Context) error {
		data, _ := io.ReadAll(c.Request().Body)
		body = string(data)
		return c.NoContent(http.StatusNoContent)
	})
	_ = handler(ctx)
	fmt.Println(body, ctx.Request().Header.Get("Content-Encoding"))
	// hello
}
