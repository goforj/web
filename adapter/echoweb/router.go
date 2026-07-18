package echoweb

import (
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/goforj/web"
	"github.com/gorilla/websocket"
	echo "github.com/labstack/echo/v5"
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

	middlewareMu      sync.RWMutex
	rootMiddlewareMu  sync.Mutex
	rootMiddleware    atomic.Pointer[rootMiddlewareHandler]
	rootMiddlewareEnd *rootMiddlewareLink

	routeHandlers       webRouteHandlers
	routeDispatcher     echo.HandlerFunc
	routeDispatcherCode uintptr
	rootWebHandler      echo.HandlerFunc
}

// rootMiddlewareContext carries Echo's request-selected handler through the
// one root middleware chain shared by every request.
type rootMiddlewareContext struct {
	*contextAdapter
	next echo.HandlerFunc
}

// echoContext returns the native request context behind a root middleware invocation.
func (c *rootMiddlewareContext) echoContext() *echo.Context {
	if c == nil || c.contextAdapter == nil {
		return nil
	}
	return c.contextAdapter.echoContext()
}

// DetachedContext creates an independently owned root invocation for asynchronous middleware.
func (c *rootMiddlewareContext) DetachedContext() (web.Context, func()) {
	detached, commit := c.contextAdapter.DetachedContext()
	return &rootMiddlewareContext{
		contextAdapter: detached.(*contextAdapter),
		next:           c.next,
	}, commit
}

// rootMiddlewareHandler holds one immutable middleware handler.
type rootMiddlewareHandler struct {
	handler web.Handler
}

// rootMiddlewareLink lets middleware registered later extend the chain without
// reconstructing middleware that has already captured application state.
type rootMiddlewareLink struct {
	next atomic.Pointer[rootMiddlewareHandler]
}

// handle invokes the current continuation for this middleware position.
func (l *rootMiddlewareLink) handle(ctx web.Context) error {
	return l.next.Load().handler(ctx)
}

// webRouteKey identifies a Web-owned handler after Echo has resolved its route pattern.
type webRouteKey struct {
	method string
	path   string
}

// webRouteHandler holds one Web-owned Echo handler so registrations can share identity safely.
type webRouteHandler struct {
	handler echo.HandlerFunc
}

// webRouteHandlers maps Echo's resolved routes and unambiguous paths to Web-owned handlers.
type webRouteHandlers struct {
	routes map[webRouteKey]*webRouteHandler
	paths  map[string]*webRouteHandler
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
	clean := cleanMiddlewares(middleware)
	if len(clean) == 0 {
		return
	}
	if r.parent == nil {
		r.appendRootMiddlewares(clean)
	}
	r.middlewareMu.Lock()
	r.middlewares = append(r.middlewares, clean...)
	r.middlewareMu.Unlock()
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
	adapted := &webRouteHandler{handler: adaptHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))}
	r.registerWebRoute(r.group.CONNECT(path, r.rootRouter().routeDispatcher), adapted)
}

// DELETE registers a DELETE route.
func (r *routerAdapter) DELETE(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := &webRouteHandler{handler: adaptHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))}
	r.registerWebRoute(r.group.DELETE(path, r.rootRouter().routeDispatcher), adapted)
}

// GET registers a GET route.
func (r *routerAdapter) GET(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := &webRouteHandler{handler: adaptHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))}
	r.registerWebRoute(r.group.GET(path, r.rootRouter().routeDispatcher), adapted)
}

// GETWS registers a GET websocket route.
func (r *routerAdapter) GETWS(path string, handler web.WebSocketHandler, middleware ...web.Middleware) {
	adapted := &webRouteHandler{handler: adaptWebSocketHandler(handler, r.routeMiddlewares(middleware)...)}
	r.registerWebRoute(r.group.GET(path, r.rootRouter().routeDispatcher), adapted)
}

// HEAD registers a HEAD route.
func (r *routerAdapter) HEAD(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := &webRouteHandler{handler: adaptHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))}
	r.registerWebRoute(r.group.HEAD(path, r.rootRouter().routeDispatcher), adapted)
}

// OPTIONS registers an OPTIONS route.
func (r *routerAdapter) OPTIONS(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := &webRouteHandler{handler: adaptHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))}
	r.registerWebRoute(r.group.OPTIONS(path, r.rootRouter().routeDispatcher), adapted)
}

// PATCH registers a PATCH route.
func (r *routerAdapter) PATCH(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := &webRouteHandler{handler: adaptHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))}
	r.registerWebRoute(r.group.PATCH(path, r.rootRouter().routeDispatcher), adapted)
}

// POST registers a POST route.
func (r *routerAdapter) POST(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := &webRouteHandler{handler: adaptHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))}
	r.registerWebRoute(r.group.POST(path, r.rootRouter().routeDispatcher), adapted)
}

// PUT registers a PUT route.
func (r *routerAdapter) PUT(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := &webRouteHandler{handler: adaptHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))}
	r.registerWebRoute(r.group.PUT(path, r.rootRouter().routeDispatcher), adapted)
}

// TRACE registers a TRACE route.
func (r *routerAdapter) TRACE(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := &webRouteHandler{handler: adaptHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))}
	r.registerWebRoute(r.group.TRACE(path, r.rootRouter().routeDispatcher), adapted)
}

// Any registers a route for every standard HTTP method.
func (r *routerAdapter) Any(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := &webRouteHandler{handler: adaptHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))}
	r.registerWebRoute(r.group.Any(path, r.rootRouter().routeDispatcher), adapted)
}

// Match registers a route for the provided set of HTTP methods.
func (r *routerAdapter) Match(methods []string, path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := &webRouteHandler{handler: adaptHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))}
	for _, route := range r.group.Match(methods, path, r.rootRouter().routeDispatcher) {
		r.registerWebRoute(route, adapted)
	}
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

// rootRouter returns the adapter that owns request-wide middleware and route dispatch.
func (r *routerAdapter) rootRouter() *routerAdapter {
	for r.parent != nil {
		r = r.parent
	}
	return r
}

// registerWebRoute publishes a Web-owned route alongside Echo's setup-only route table.
func (r *routerAdapter) registerWebRoute(route echo.RouteInfo, handler *webRouteHandler) {
	root := r.rootRouter()
	root.routeHandlers.routes[webRouteKey{method: route.Method, path: route.Path}] = handler
	current, registered := root.routeHandlers.paths[route.Path]
	if !registered {
		root.routeHandlers.paths[route.Path] = handler
	} else if current != handler {
		// A nil path entry records that Echo's selected method is required to
		// distinguish multiple handlers mounted on the same route pattern.
		root.routeHandlers.paths[route.Path] = nil
	}
}

// dispatchWebRoute invokes the Web handler associated with Echo's resolved route.
func (r *routerAdapter) dispatchWebRoute(c *echo.Context) error {
	path := selectedWebRoutePath(c)
	if handler, ok := r.routeHandlers.paths[path]; ok && handler != nil {
		return handler.handler(c)
	}
	selected := c.RouteInfo()
	handler, ok := r.routeHandlers.routes[webRouteKey{method: selected.Method, path: path}]
	if !ok {
		return echo.ErrInternalServerError
	}
	return handler.handler(c)
}

// selectedWebRoutePath returns Echo's immutable route selection when middleware changed a public path view.
func selectedWebRoutePath(c *echo.Context) string {
	path := c.Path()
	request := c.Request()
	if request != nil && request.Pattern != "" && request.Pattern == path {
		return path
	}
	return c.RouteInfo().Path
}

// adaptRouterMiddlewares bridges web middleware into Echo middleware for a router scope.
func adaptRouterMiddlewares(r *routerAdapter) echo.MiddlewareFunc {
	r.routeHandlers = webRouteHandlers{
		routes: make(map[webRouteKey]*webRouteHandler),
		paths:  make(map[string]*webRouteHandler),
	}
	r.routeDispatcher = r.dispatchWebRoute
	r.routeDispatcherCode = reflect.ValueOf(r.routeDispatcher).Pointer()
	r.rootWebHandler = r.dispatchRootWebRoute
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return r.rootMiddlewareHandler(next)
	}
}

// rootMiddlewareHandler binds Echo's request-selected continuation to the shared middleware chain.
func (r *routerAdapter) rootMiddlewareHandler(next echo.HandlerFunc) echo.HandlerFunc {
	head := r.rootMiddleware.Load()
	if head == nil {
		return next
	}
	// Native middleware and additional wrapped adapters can obscure which
	// receiver owns a method-value code pointer, so they use the dynamic path.
	if len(r.engine.Middlewares()) == 1 && reflect.ValueOf(next).Pointer() == r.routeDispatcherCode {
		return r.rootWebHandler
	}

	return func(c *echo.Context) error {
		ctx := rootMiddlewareContext{
			contextAdapter: acquireContextAdapter(c),
			next:           next,
		}
		return head.handler(&ctx)
	}
}

// dispatchRootWebRoute runs the allocation-free path for a Web-owned route.
func (r *routerAdapter) dispatchRootWebRoute(c *echo.Context) error {
	head := r.rootMiddleware.Load()
	if head == nil {
		return r.dispatchWebRoute(c)
	}
	return head.handler(acquireContextAdapter(c))
}

// appendRootMiddlewares extends the shared chain without reconstructing existing middleware.
func (r *routerAdapter) appendRootMiddlewares(middleware []web.Middleware) {
	r.rootMiddlewareMu.Lock()
	defer r.rootMiddlewareMu.Unlock()

	head, end := r.compileRootMiddlewareSegment(middleware)
	if head == nil {
		return
	}
	if r.rootMiddlewareEnd == nil {
		r.rootMiddleware.Store(head)
	} else {
		r.rootMiddlewareEnd.next.Store(head)
	}
	r.rootMiddlewareEnd = end
}

// compileRootMiddlewareSegment constructs each middleware exactly once and preserves registration order.
func (r *routerAdapter) compileRootMiddlewareSegment(middleware []web.Middleware) (*rootMiddlewareHandler, *rootMiddlewareLink) {
	terminal := &rootMiddlewareHandler{handler: r.invokeRootMiddlewareNext}
	var head *rootMiddlewareHandler
	var end *rootMiddlewareLink
	for _, item := range middleware {
		link := &rootMiddlewareLink{}
		link.next.Store(terminal)
		current := &rootMiddlewareHandler{handler: item(link.handle)}
		if head == nil {
			head = current
		} else {
			end.next.Store(current)
		}
		end = link
	}
	return head, end
}

// invokeRootMiddlewareNext continues through the Echo middleware and route selected for this request.
func (r *routerAdapter) invokeRootMiddlewareNext(ctx web.Context) error {
	if root, ok := ctx.(*rootMiddlewareContext); ok {
		if root == nil || root.next == nil || root.echoContext() == nil {
			return echo.ErrInternalServerError
		}
		return root.next(root.echoContext())
	}
	adapted, ok := ctx.(*contextAdapter)
	if !ok || adapted == nil {
		return echo.ErrInternalServerError
	}
	return r.dispatchWebRoute(adapted.echoContext())
}

// middlewareSnapshot returns an immutable copy of the current middleware configuration.
func (r *routerAdapter) middlewareSnapshot() []web.Middleware {
	if r == nil {
		return nil
	}
	r.middlewareMu.RLock()
	defer r.middlewareMu.RUnlock()
	return append([]web.Middleware(nil), r.middlewares...)
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
	current := r.middlewareSnapshot()
	cleanCurrent := cleanMiddlewares(current)
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

// adaptHandler converts a web handler into an Echo handler.
func adaptHandler(handler web.Handler) echo.HandlerFunc {
	return func(c *echo.Context) error {
		return handler(acquireContextAdapter(c))
	}
}

// adaptWebSocketHandler compiles HTTP middleware around a websocket upgrade and handler.
func adaptWebSocketHandler(handler web.WebSocketHandler, middleware ...web.Middleware) echo.HandlerFunc {
	applied := applyMiddlewares(func(ctx web.Context) error {
		native, ok := UnwrapContext(ctx)
		if !ok {
			return echo.ErrInternalServerError
		}
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(native.Response(), native.Request(), nil)
		if err != nil {
			return err
		}
		return handler(ctx, newWebSocketConn(conn))
	}, middleware...)
	return adaptHandler(applied)
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
	adapted := applyMiddlewares(func(r web.Context) error {
		root, ok := r.(*rootMiddlewareContext)
		if !ok || root == nil || root.next == nil || root.echoContext() == nil {
			return echo.ErrInternalServerError
		}
		return root.next(root.echoContext())
	}, middlewares...)

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			ctx := rootMiddlewareContext{
				contextAdapter: acquireContextAdapter(c),
				next:           next,
			}
			return adapted(&ctx)
		}
	}
}
