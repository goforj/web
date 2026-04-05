package echoweb

import (
	"github.com/goforj/web"
	"github.com/gorilla/websocket"
	echo "github.com/labstack/echo/v4"
)

type groupLike interface {
	Use(middleware ...echo.MiddlewareFunc)
	CONNECT(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
	DELETE(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
	GET(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
	HEAD(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
	OPTIONS(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
	POST(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
	PUT(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
	PATCH(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
	TRACE(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
	Any(path string, handler echo.HandlerFunc, middleware ...echo.MiddlewareFunc) []*echo.Route
	Match(methods []string, path string, handler echo.HandlerFunc, middleware ...echo.MiddlewareFunc) []*echo.Route
	Group(prefix string, middleware ...echo.MiddlewareFunc) *echo.Group
}

type routerAdapter struct {
	engine      *echo.Echo
	group       groupLike
	parent      *routerAdapter
	middlewares []web.Middleware
}

var _ web.Router = (*routerAdapter)(nil)

func (r *routerAdapter) Pre(middleware ...web.Middleware) {
	if r.engine == nil {
		return
	}
	r.engine.Pre(mustAdaptMiddlewares(middleware)...)
}

func (r *routerAdapter) Use(middleware ...web.Middleware) {
	r.middlewares = append(r.middlewares, cleanMiddlewares(middleware)...)
}

func (r *routerAdapter) CONNECT(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.CONNECT(path, adaptHandler(applyMiddlewares(handler, middleware...)))
}

func (r *routerAdapter) DELETE(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.DELETE(path, adaptHandler(applyMiddlewares(handler, middleware...)))
}

func (r *routerAdapter) GET(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.GET(path, adaptHandler(applyMiddlewares(handler, middleware...)))
}

func (r *routerAdapter) GETWS(path string, handler web.WebSocketHandler, middleware ...web.Middleware) {
	r.group.GET(path, adaptWebSocketHandler(applyWebSocketMiddlewares(handler, middleware...)))
}

func (r *routerAdapter) HEAD(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.HEAD(path, adaptHandler(applyMiddlewares(handler, middleware...)))
}

func (r *routerAdapter) OPTIONS(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.OPTIONS(path, adaptHandler(applyMiddlewares(handler, middleware...)))
}

func (r *routerAdapter) PATCH(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.PATCH(path, adaptHandler(applyMiddlewares(handler, middleware...)))
}

func (r *routerAdapter) POST(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.POST(path, adaptHandler(applyMiddlewares(handler, middleware...)))
}

func (r *routerAdapter) PUT(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.PUT(path, adaptHandler(applyMiddlewares(handler, middleware...)))
}

func (r *routerAdapter) TRACE(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.TRACE(path, adaptHandler(applyMiddlewares(handler, middleware...)))
}

func (r *routerAdapter) Any(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.Any(path, adaptHandler(applyMiddlewares(handler, middleware...)))
}

func (r *routerAdapter) Match(methods []string, path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.Match(methods, path, adaptHandler(applyMiddlewares(handler, middleware...)))
}

func (r *routerAdapter) Group(prefix string, middleware ...web.Middleware) web.Router {
	child := &routerAdapter{
		engine:      r.engine,
		parent:      r,
		middlewares: cleanMiddlewares(middleware),
	}
	child.group = r.group.Group(prefix, adaptRouterMiddlewares(child))
	return child
}

func adaptRouterMiddlewares(r *routerAdapter) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if len(r.middlewares) == 0 {
				return next(c)
			}
			adaptedCtx := acquireContextAdapter(c)
			defer releaseContextAdapter(adaptedCtx)
			adapted := func(ctx web.Context) error {
				native, ok := UnwrapContext(ctx)
				if !ok {
					return echo.ErrInternalServerError
				}
				return next(native)
			}
			return applyMiddlewares(adapted, r.middlewares...)(adaptedCtx)
		}
	}
}

func applyMiddlewares(handler web.Handler, middleware ...web.Middleware) web.Handler {
	applied := handler
	clean := cleanMiddlewares(middleware)
	for i := len(clean) - 1; i >= 0; i-- {
		applied = clean[i](applied)
	}
	return applied
}

func applyWebSocketMiddlewares(handler web.WebSocketHandler, middleware ...web.Middleware) web.WebSocketHandler {
	return func(ctx web.Context, conn web.WebSocketConn) error {
		return applyMiddlewares(func(inner web.Context) error {
			return handler(inner, conn)
		}, middleware...)(ctx)
	}
}

func adaptHandler(handler web.Handler) echo.HandlerFunc {
	return func(c echo.Context) error {
		adapted := acquireContextAdapter(c)
		defer releaseContextAdapter(adapted)
		return handler(adapted)
	}
}

func adaptWebSocketHandler(handler web.WebSocketHandler) echo.HandlerFunc {
	return func(c echo.Context) error {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		adapted := acquireContextAdapter(c)
		defer releaseContextAdapter(adapted)
		return handler(adapted, newWebSocketConn(conn))
	}
}

func cleanMiddlewares(middleware []web.Middleware) []web.Middleware {
	if len(middleware) == 0 {
		return nil
	}
	clean := make([]web.Middleware, 0, len(middleware))
	for _, item := range middleware {
		if item == nil {
			continue
		}
		clean = append(clean, item)
	}
	return clean
}

func mustAdaptMiddlewares(middleware []web.Middleware) []echo.MiddlewareFunc {
	clean := cleanMiddlewares(middleware)
	if len(clean) == 0 {
		return nil
	}
	return []echo.MiddlewareFunc{adaptMiddlewares(clean)}
}

func adaptMiddlewares(middlewares []web.Middleware) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		adapted := func(r web.Context) error {
			native, ok := UnwrapContext(r)
			if !ok {
				return echo.ErrInternalServerError
			}
			return next(native)
		}
		for i := len(middlewares) - 1; i >= 0; i-- {
			adapted = middlewares[i](adapted)
		}

		return func(c echo.Context) error {
			adaptedCtx := acquireContextAdapter(c)
			defer releaseContextAdapter(adaptedCtx)
			return adapted(adaptedCtx)
		}
	}
}
