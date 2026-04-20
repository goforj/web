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
	var captured string
	mw := webmiddleware.BodyDump(func(c web.Context, reqBody, resBody []byte) {
		captured = fmt.Sprintf("%s -> %s", string(reqBody), string(resBody))
	})
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("ping"))
	ctx := webtest.NewContext(req, nil, "/", nil)
	handler := mw(func(c web.Context) error { return c.Text(http.StatusOK, "pong") })
	_ = handler(ctx)
	fmt.Println(captured)
	// ping -> pong
}
