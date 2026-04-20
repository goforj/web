package main

import (
	"github.com/goforj/web/webmiddleware"
	"net/http"
)

func main() {
	_ = webmiddleware.WWWRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})
	// true
}
