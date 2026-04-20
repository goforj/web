package main

import (
	"github.com/goforj/web/webmiddleware"
	"net/http"
)

func main() {
	_ = webmiddleware.HTTPSNonWWWRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})
	// true
}
