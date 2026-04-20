package main

import (
	"github.com/goforj/web/webmiddleware"
)

func main() {
	_ = webmiddleware.RemoveTrailingSlashWithConfig(webmiddleware.TrailingSlashConfig{RedirectCode: 308})
}
