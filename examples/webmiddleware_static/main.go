package main

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	dir, _ := os.MkdirTemp("", "web-static-*")
	defer os.RemoveAll(dir)
	_ = os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o644)
	req := httptest.NewRequest(http.MethodGet, "/hello.txt", nil)
	ctx := webtest.NewContext(req, nil, "/hello.txt", nil)
	_ = webmiddleware.Static(dir)(func(c web.Context) error { return c.NoContent(http.StatusNotFound) })(ctx)
	fmt.Println(strings.TrimSpace(ctx.ResponseWriter().(*httptest.ResponseRecorder).Body.String()))
	// hello
}
