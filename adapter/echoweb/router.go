package echoweb

import (
	"github.com/goforj/web"
	echo "github.com/labstack/echo/v4"
)

type groupLike interface {
	Use(middleware ...echo.MiddlewareFunc)
	GET(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
	POST(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
	PUT(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
	PATCH(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
	DELETE(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
	Group(prefix string, middleware ...echo.MiddlewareFunc) *echo.Group
}

type routerAdapter struct {
	group groupLike
}

var _ web.Router = (*routerAdapter)(nil)

func (r *routerAdapter) Use(middleware ...web.Middleware) {
	r.group.Use(mustAdaptMiddlewares(middleware)...)
}

func (r *routerAdapter) Get(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.GET(path, adaptHandler(handler), mustAdaptMiddlewares(middleware)...)
}

func (r *routerAdapter) Post(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.POST(path, adaptHandler(handler), mustAdaptMiddlewares(middleware)...)
}

func (r *routerAdapter) Put(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.PUT(path, adaptHandler(handler), mustAdaptMiddlewares(middleware)...)
}

func (r *routerAdapter) Patch(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.PATCH(path, adaptHandler(handler), mustAdaptMiddlewares(middleware)...)
}

func (r *routerAdapter) Delete(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.DELETE(path, adaptHandler(handler), mustAdaptMiddlewares(middleware)...)
}

func (r *routerAdapter) Group(prefix string, middleware ...web.Middleware) web.Router {
	return &routerAdapter{group: r.group.Group(prefix, mustAdaptMiddlewares(middleware)...)}
}

func adaptHandler(handler web.Handler) echo.HandlerFunc {
	return func(c echo.Context) error {
		return handler(newContextAdapter(c))
	}
}

func mustAdaptMiddlewares(middleware []web.Middleware) []echo.MiddlewareFunc {
	if len(middleware) == 0 {
		return nil
	}
	out := make([]echo.MiddlewareFunc, 0, len(middleware))
	for _, item := range middleware {
		if item == nil {
			continue
		}
		out = append(out, adaptMiddleware(item))
	}
	return out
}

func adaptMiddleware(middleware web.Middleware) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			adapted := middleware(func(r web.Context) error {
				native, ok := UnwrapContext(r)
				if !ok {
					return next(c)
				}
				return next(native)
			})
			return adapted(newContextAdapter(c))
		}
	}
}
