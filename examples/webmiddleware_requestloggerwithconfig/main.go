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
	var loggedURI string
	mw := webmiddleware.RequestLoggerWithConfig(webmiddleware.RequestLoggerConfig{
		LogValuesFunc: func(c web.Context, values webmiddleware.RequestLoggerValues) error {
			loggedURI = values.URI
			return nil
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	ctx := webtest.NewContext(req, nil, "/users/:id", webtest.PathParams{"id": "42"})
	handler := mw(func(c web.Context) error { return c.NoContent(http.StatusAccepted) })
	_ = handler(ctx)
	fmt.Println(loggedURI, ctx.StatusCode())
	// /users/42 202
}
