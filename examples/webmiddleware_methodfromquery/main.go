package main

import (
	"fmt"
	"github.com/goforj/web/webmiddleware"
	"github.com/goforj/web/webtest"
	"net/http"
	"net/http/httptest"
)

func main() {
	getter := webmiddleware.MethodFromQuery("_method")
	req := httptest.NewRequest(http.MethodPost, "/?_method=PUT", nil)
	ctx := webtest.NewContext(req, nil, "/", nil)
	fmt.Println(getter(ctx))
	// PUT
}
