package echoweb

import (
	"errors"

	"github.com/goforj/web"
	echo "github.com/labstack/echo/v4"
)

// Deprecated: WrapHandler is a legacy migration bridge for Echo handlers.
// New application code should use web.Handler directly.
func WrapHandler(handler echo.HandlerFunc) web.Handler {
	return func(r web.Context) error {
		native, ok := UnwrapContext(r)
		if !ok {
			return errors.New("echo: context does not originate from echo adapter")
		}
		return handler(native)
	}
}

// Deprecated: WrapMiddleware is a legacy migration bridge for Echo middleware.
// New application code should use webmiddleware or plain web.Middleware directly.
func WrapMiddleware(middleware echo.MiddlewareFunc) web.Middleware {
	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			native, ok := UnwrapContext(r)
			if !ok {
				return next(r)
			}
			handler := middleware(func(c echo.Context) error {
				adapted := acquireContextAdapter(c)
				defer releaseContextAdapter(adapted)
				return next(adapted)
			})
			return handler(native)
		}
	}
}
