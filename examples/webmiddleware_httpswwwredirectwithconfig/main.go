package main

import (
	"github.com/goforj/web/webmiddleware"
	"net/http"
)

func main() {
	_ = webmiddleware.HTTPSWWWRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})
	// true
}
