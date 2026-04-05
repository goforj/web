package webmiddleware

import "github.com/goforj/web"

// Skipper skips middleware processing when it returns true.
type Skipper func(web.Context) bool

// DefaultSkipper always runs the middleware.
func DefaultSkipper(web.Context) bool {
	return false
}
