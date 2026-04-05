package webecho

import (
	"errors"

	"github.com/goforj/web"
	echo "github.com/labstack/echo/v4"
)

// WrapHandler exposes a native Echo handler as a web.Handler bridge.
func WrapHandler(handler echo.HandlerFunc) web.Handler {
	return func(r web.Context) error {
		native, ok := UnwrapContext(r)
		if !ok {
			return errors.New("echo: context does not originate from echo adapter")
		}
		return handler(native)
	}
}

// WrapMiddleware exposes a native Echo middleware as a web.Middleware bridge.
func WrapMiddleware(middleware echo.MiddlewareFunc) web.Middleware {
	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			native, ok := UnwrapContext(r)
			if !ok {
				return next(r)
			}
			handler := middleware(func(c echo.Context) error {
				return next(newContextAdapter(c))
			})
			return handler(native)
		}
	}
}
