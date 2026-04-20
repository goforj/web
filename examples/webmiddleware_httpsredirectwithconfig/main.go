package main

import (
	"github.com/goforj/web/webmiddleware"
	"net/http"
)

func main() {
	_ = webmiddleware.HTTPSRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})
}
