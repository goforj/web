package main

import (
	"fmt"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"net/http"
	"net/http/httptest"
	"strings"
)

func main() {
	getter := webmiddleware.MethodFromForm("_method")
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("_method=DELETE"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := webtest.NewContext(req, nil, "/", nil)
	fmt.Println(getter(ctx))
	// DELETE
}
