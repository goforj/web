package echoweb

import (
	"fmt"
	"github.com/goforj/web"
	"github.com/gorilla/websocket"
	echo "github.com/labstack/echo/v5"
	"net/http"
	"sync"
	"unsafe"
)

type groupLike interface {
	Use(middleware ...echo.MiddlewareFunc)
	CONNECT(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) echo.RouteInfo
	DELETE(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) echo.RouteInfo
	GET(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) echo.RouteInfo
	HEAD(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) echo.RouteInfo
	OPTIONS(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) echo.RouteInfo
	POST(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) echo.RouteInfo
	PUT(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) echo.RouteInfo
	PATCH(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) echo.RouteInfo
	TRACE(path string, h echo.HandlerFunc, middleware ...echo.MiddlewareFunc) echo.RouteInfo
	Any(path string, handler echo.HandlerFunc, middleware ...echo.MiddlewareFunc) echo.RouteInfo
	Match(methods []string, path string, handler echo.HandlerFunc, middleware ...echo.MiddlewareFunc) echo.Routes
	Group(prefix string, middleware ...echo.MiddlewareFunc) *echo.Group
}

type routerAdapter struct {
	engine      *echo.Echo
	group       groupLike
	parent      *routerAdapter
	middlewares []web.Middleware
}

// rootAdapterReuseHandler lets root-mounted middleware hand the already-adapted
// request context directly to route handlers, avoiding per-request Echo Set/Get
// state handoff on matched routes.
type rootAdapterReuseHandler func(*echo.Context, *contextAdapter) error

type handlerIdentity struct {
	code uintptr
	data uintptr
}

var rootAdapterReuseHandlers sync.Map

// registerRootAdapterReuseHandler stores the direct-handler fast path for a wrapped Echo handler.
func registerRootAdapterReuseHandler(handler echo.HandlerFunc, direct rootAdapterReuseHandler) {
	if handler == nil || direct == nil {
		return
	}
	rootAdapterReuseHandlers.Store(handlerIdentityFor(handler), direct)
}

// lookupRootAdapterReuseHandler returns a previously registered direct-handler fast path.
func lookupRootAdapterReuseHandler(handler echo.HandlerFunc) (rootAdapterReuseHandler, bool) {
	if handler == nil {
		return nil, false
	}
	direct, ok := rootAdapterReuseHandlers.Load(handlerIdentityFor(handler))
	if !ok {
		return nil, false
	}
	typed, ok := direct.(rootAdapterReuseHandler)
	if !ok || typed == nil {
		return nil, false
	}
	return typed, true
}

// handlerIdentityFor derives a stable identity key for an Echo handler closure.
func handlerIdentityFor(handler echo.HandlerFunc) handlerIdentity {
	// Echo handlers are closures, so pointer equality on the function value is
	// the cheapest stable key we have for the root-middleware reuse registry.
	words := *(*[2]uintptr)(unsafe.Pointer(&handler))
	return handlerIdentity{
		code: words[0],
		data: words[1],
	}
}

var _ web.Router = (*routerAdapter)(nil)

// Pre registers Echo-native pre-routing middleware on the engine.
func (r *routerAdapter) Pre(middleware ...web.Middleware) {
	if r.engine == nil {
		return
	}
	r.engine.Pre(mustAdaptMiddlewares(middleware)...)
}

// Use registers middleware on the current router scope.
func (r *routerAdapter) Use(middleware ...web.Middleware) {
	r.middlewares = append(r.middlewares, cleanMiddlewares(middleware)...)
}

// Handle dispatches a route registration by HTTP method.
func (r *routerAdapter) Handle(method string, path string, handler web.Handler, middleware ...web.Middleware) error {
	switch method {
	case http.MethodConnect:
		r.CONNECT(path, handler, middleware...)
	case http.MethodDelete:
		r.DELETE(path, handler, middleware...)
	case http.MethodGet:
		r.GET(path, handler, middleware...)
	case http.MethodHead:
		r.HEAD(path, handler, middleware...)
	case http.MethodOptions:
		r.OPTIONS(path, handler, middleware...)
	case http.MethodPatch:
		r.PATCH(path, handler, middleware...)
	case http.MethodPost:
		r.POST(path, handler, middleware...)
	case http.MethodPut:
		r.PUT(path, handler, middleware...)
	case http.MethodTrace:
		r.TRACE(path, handler, middleware...)
	default:
		return fmt.Errorf("unsupported route method %q", method)
	}
	return nil
}

// CONNECT registers a CONNECT route.
func (r *routerAdapter) CONNECT(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.CONNECT(path, adaptHandlerForRouter(r, applyMiddlewares(handler, r.routeMiddlewares(middleware)...)))
}

// DELETE registers a DELETE route.
func (r *routerAdapter) DELETE(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.DELETE(path, adaptHandlerForRouter(r, applyMiddlewares(handler, r.routeMiddlewares(middleware)...)))
}

// GET registers a GET route.
func (r *routerAdapter) GET(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.GET(path, adaptHandlerForRouter(r, applyMiddlewares(handler, r.routeMiddlewares(middleware)...)))
}

// GETWS registers a GET websocket route.
func (r *routerAdapter) GETWS(path string, handler web.WebSocketHandler, middleware ...web.Middleware) {
	r.group.GET(path, adaptWebSocketHandlerForRouter(r, applyWebSocketMiddlewares(handler, r.routeMiddlewares(middleware)...)))
}

// HEAD registers a HEAD route.
func (r *routerAdapter) HEAD(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.HEAD(path, adaptHandlerForRouter(r, applyMiddlewares(handler, r.routeMiddlewares(middleware)...)))
}

// OPTIONS registers an OPTIONS route.
func (r *routerAdapter) OPTIONS(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.OPTIONS(path, adaptHandlerForRouter(r, applyMiddlewares(handler, r.routeMiddlewares(middleware)...)))
}

// PATCH registers a PATCH route.
func (r *routerAdapter) PATCH(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.PATCH(path, adaptHandlerForRouter(r, applyMiddlewares(handler, r.routeMiddlewares(middleware)...)))
}

// POST registers a POST route.
func (r *routerAdapter) POST(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.POST(path, adaptHandlerForRouter(r, applyMiddlewares(handler, r.routeMiddlewares(middleware)...)))
}

// PUT registers a PUT route.
func (r *routerAdapter) PUT(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.PUT(path, adaptHandlerForRouter(r, applyMiddlewares(handler, r.routeMiddlewares(middleware)...)))
}

// TRACE registers a TRACE route.
func (r *routerAdapter) TRACE(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.TRACE(path, adaptHandlerForRouter(r, applyMiddlewares(handler, r.routeMiddlewares(middleware)...)))
}

// Any registers a route for every standard HTTP method.
func (r *routerAdapter) Any(path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.Any(path, adaptHandlerForRouter(r, applyMiddlewares(handler, r.routeMiddlewares(middleware)...)))
}

// Match registers a route for the provided set of HTTP methods.
func (r *routerAdapter) Match(methods []string, path string, handler web.Handler, middleware ...web.Middleware) {
	r.group.Match(methods, path, adaptHandlerForRouter(r, applyMiddlewares(handler, r.routeMiddlewares(middleware)...)))
}

// Group creates a child router with a prefixed path scope.
func (r *routerAdapter) Group(prefix string, middleware ...web.Middleware) web.Router {
	child := &routerAdapter{
		engine:      r.engine,
		parent:      r,
		middlewares: cleanMiddlewares(middleware),
	}
	child.group = r.group.Group(prefix)
	return child
}

// adaptRouterMiddlewares bridges web middleware into Echo middleware for a router scope.
func adaptRouterMiddlewares(r *routerAdapter) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		if len(r.middlewares) == 0 {
			return next
		}

		reuseDirect, reuseAvailable := lookupRootAdapterReuseHandler(next)
		adapted := func(ctx web.Context) error {
			if reuseAvailable {
				// When root middleware wraps a matched route, reuse the already
				// adapted request context instead of bouncing through Echo Set/Get.
				adaptedCtx, ok := ctx.(*contextAdapter)
				if !ok || adaptedCtx == nil {
					return echo.ErrInternalServerError
				}
				native, ok := UnwrapContext(ctx)
				if !ok {
					return echo.ErrInternalServerError
				}
				return reuseDirect(native, adaptedCtx)
			}
			native, ok := UnwrapContext(ctx)
			if !ok {
				return echo.ErrInternalServerError
			}
			return next(native)
		}
		applied := applyMiddlewares(adapted, r.middlewares...)

		return func(c *echo.Context) error {
			adaptedCtx := acquireContextAdapter(c)
			defer releaseContextAdapter(adaptedCtx)
			return applied(adaptedCtx)
		}
	}
}

// routeMiddlewares returns the middleware chain that applies to one concrete route.
func (r *routerAdapter) routeMiddlewares(route []web.Middleware) []web.Middleware {
	inherited := r.inheritedRouteMiddlewares()
	if len(inherited) == 0 {
		return cleanMiddlewares(route)
	}
	cleanRoute := cleanMiddlewares(route)
	all := make([]web.Middleware, 0, len(inherited)+len(cleanRoute))
	all = append(all, inherited...)
	all = append(all, cleanRoute...)
	return all
}

// inheritedRouteMiddlewares returns inherited non-root middleware for child routers.
func (r *routerAdapter) inheritedRouteMiddlewares() []web.Middleware {
	if r == nil {
		return nil
	}
	if r.parent == nil {
		// Root router middleware is mounted at the Echo engine level so it can
		// still intercept unmatched paths and method-mismatch preflights.
		return nil
	}
	parent := r.parent.inheritedRouteMiddlewares()
	cleanCurrent := cleanMiddlewares(r.middlewares)
	if len(parent) == 0 {
		return cleanCurrent
	}
	if len(cleanCurrent) == 0 {
		return parent
	}
	all := make([]web.Middleware, 0, len(parent)+len(cleanCurrent))
	all = append(all, parent...)
	all = append(all, cleanCurrent...)
	return all
}

// applyMiddlewares wraps a handler in the provided middleware chain.
func applyMiddlewares(handler web.Handler, middleware ...web.Middleware) web.Handler {
	applied := handler
	clean := cleanMiddlewares(middleware)
	for i := len(clean) - 1; i >= 0; i-- {
		applied = clean[i](applied)
	}
	return applied
}

// applyWebSocketMiddlewares adapts standard middleware around a websocket handler.
func applyWebSocketMiddlewares(handler web.WebSocketHandler, middleware ...web.Middleware) web.WebSocketHandler {
	return func(ctx web.Context, conn web.WebSocketConn) error {
		return applyMiddlewares(func(inner web.Context) error {
			return handler(inner, conn)
		}, middleware...)(ctx)
	}
}

// adaptHandler converts a web handler into an Echo handler.
func adaptHandler(handler web.Handler) echo.HandlerFunc {
	return func(c *echo.Context) error {
		adapted := acquireContextAdapter(c)
		defer releaseContextAdapter(adapted)
		return handler(adapted)
	}
}

// adaptHandlerForRouter converts a routed web handler into an Echo handler with reuse fast paths.
func adaptHandlerForRouter(r *routerAdapter, handler web.Handler) echo.HandlerFunc {
	direct := func(c *echo.Context, adapted *contextAdapter) error {
		if adapted == nil {
			return echo.ErrInternalServerError
		}
		return handler(adapted)
	}

	wrapped := func(c *echo.Context) error {
		adapted := acquireContextAdapter(c)
		defer releaseContextAdapter(adapted)
		return direct(c, adapted)
	}
	if r.hasRootMiddleware() {
		registerRootAdapterReuseHandler(wrapped, direct)
	}
	return wrapped
}

// adaptWebSocketHandler converts a websocket handler into an Echo handler.
func adaptWebSocketHandler(handler web.WebSocketHandler) echo.HandlerFunc {
	return func(c *echo.Context) error {
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

// adaptWebSocketHandlerForRouter converts a routed websocket handler into an Echo handler.
func adaptWebSocketHandlerForRouter(r *routerAdapter, handler web.WebSocketHandler) echo.HandlerFunc {
	direct := func(c *echo.Context, adapted *contextAdapter) error {
		if adapted == nil {
			return echo.ErrInternalServerError
		}
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		return handler(adapted, newWebSocketConn(conn))
	}

	wrapped := func(c *echo.Context) error {
		adapted := acquireContextAdapter(c)
		defer releaseContextAdapter(adapted)
		return direct(c, adapted)
	}
	if r.hasRootMiddleware() {
		registerRootAdapterReuseHandler(wrapped, direct)
	}
	return wrapped
}

// hasRootMiddleware reports whether this router inherits root-mounted middleware.
func (r *routerAdapter) hasRootMiddleware() bool {
	if r == nil {
		return false
	}
	for current := r; current != nil; current = current.parent {
		// Only root-level middleware needs the reuse hook because group/route
		// middleware is already flattened into the final route handler.
		if current.parent == nil {
			return len(current.middlewares) > 0
		}
	}
	return false
}

// cleanMiddlewares drops nil entries while preserving middleware order.
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

// mustAdaptMiddlewares adapts middleware and panics if adaptation fails.
func mustAdaptMiddlewares(middleware []web.Middleware) []echo.MiddlewareFunc {
	clean := cleanMiddlewares(middleware)
	if len(clean) == 0 {
		return nil
	}
	return []echo.MiddlewareFunc{adaptMiddlewares(clean)}
}

// adaptMiddlewares converts web middleware into a single Echo middleware.
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

		return func(c *echo.Context) error {
			adaptedCtx := acquireContextAdapter(c)
			defer releaseContextAdapter(adaptedCtx)
			return adapted(adaptedCtx)
		}
	}
}
