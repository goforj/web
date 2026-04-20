package main

import (
	"fmt"
	"github.com/goforj/web/webtest"
	"net/http"
	"net/http/httptest"
)

func main() {
	req := httptest.NewRequest(http.MethodGet, "/users/42?expand=roles", nil)
	ctx := webtest.NewContext(req, nil, "/users/:id", webtest.PathParams{"id": "42"})
	fmt.Println(ctx.Param("id"), ctx.Query("expand"))
	// 42 roles
}
