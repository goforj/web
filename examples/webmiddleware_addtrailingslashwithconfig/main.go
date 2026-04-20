package main

import (
	"github.com/goforj/web/webmiddleware"
)

func main() {
	_ = webmiddleware.AddTrailingSlashWithConfig(webmiddleware.TrailingSlashConfig{RedirectCode: 308})
}
