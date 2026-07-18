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
	rootContexts      sync.Pool

	webRouteHandlerCode uintptr
	directWebRoutes     bool
}

// rootMiddlewareContext carries Echo's request-selected handler through the
// one root middleware chain shared by every request.
type rootMiddlewareContext struct {
	*contextAdapter
	nextEcho    echo.HandlerFunc
	nextWeb     web.Handler
	stateKey    string
	stateValue  any
	stateSet    bool
	nativeState bool
}

// echoContext returns the native request context behind a root middleware invocation.
func (c *rootMiddlewareContext) echoContext() *echo.Context {
	if c == nil || c.contextAdapter == nil {
		return nil
	}
	c.promoteStateToNative()
	return c.contextAdapter.echoContext()
}

// Set keeps one request value inline until interoperability requires Echo's general-purpose store.
func (c *rootMiddlewareContext) Set(key string, value any) {
	if key == contextAdapterStateKey || key == contextAdapterTrackedKeysKey || key == contextAdapterTimeoutStateKey {
		c.contextAdapter.echoContext().Set(key, value)
		return
	}
	if c.nativeState {
		c.contextAdapter.Set(key, value)
		return
	}
	if !c.stateSet || c.stateKey == key {
		c.stateKey = key
		c.stateValue = value
		c.stateSet = true
		return
	}
	c.promoteStateToNative()
	c.contextAdapter.Set(key, value)
}

// Get reads inline request state before consulting Echo and detached Timeout fallbacks.
func (c *rootMiddlewareContext) Get(key string) any {
	if c.stateSet && c.stateKey == key {
		return c.stateValue
	}
	return c.contextAdapter.Get(key)
}

// promoteStateToNative preserves Web state before code can observe or retain the native Echo context.
func (c *rootMiddlewareContext) promoteStateToNative() {
	if c == nil || c.contextAdapter == nil {
		return
	}
	if c.stateSet {
		c.contextAdapter.Set(c.stateKey, c.stateValue)
		c.stateKey = ""
		c.stateValue = nil
		c.stateSet = false
	}
	c.nativeState = true
}

// Native returns the underlying Echo context after synchronizing inline request state.
func (c *rootMiddlewareContext) Native() any {
	return c.echoContext()
}

// Bind synchronizes inline request state before invoking Echo's configured binder.
func (c *rootMiddlewareContext) Bind(target any) error {
	c.promoteStateToNative()
	return c.contextAdapter.Bind(target)
}

// JSON synchronizes inline request state before invoking Echo's configured serializer.
func (c *rootMiddlewareContext) JSON(code int, payload any) error {
	c.promoteStateToNative()
	return c.contextAdapter.JSON(code, payload)
}

// DetachedContext creates an independently owned root invocation for asynchronous middleware.
func (c *rootMiddlewareContext) DetachedContext() (web.Context, func()) {
	c.promoteStateToNative()
	detached, commit := c.contextAdapter.DetachedContext()
	detachedContext := &rootMiddlewareContext{
		contextAdapter: detached.(*contextAdapter),
		nextEcho:       c.nextEcho,
		nextWeb:        c.nextWeb,
	}
	return detachedContext, func() {
		detachedContext.promoteStateToNative()
		commit()
	}
}

// acquireRootContext reuses the short-lived state carrier needed by root middleware and Timeout snapshots.
func (r *routerAdapter) acquireRootContext(c *echo.Context, nextEcho echo.HandlerFunc, nextWeb web.Handler) *rootMiddlewareContext {
	ctx, _ := r.rootContexts.Get().(*rootMiddlewareContext)
	if ctx == nil {
		ctx = &rootMiddlewareContext{}
	}
	ctx.contextAdapter = acquireContextAdapter(c)
	ctx.nextEcho = nextEcho
	ctx.nextWeb = nextWeb
	return ctx
}

// releaseRootContext clears request ownership before returning a middleware state carrier to the pool.
func (r *routerAdapter) releaseRootContext(ctx *rootMiddlewareContext) {
	ctx.contextAdapter = nil
	ctx.nextEcho = nil
	ctx.nextWeb = nil
	ctx.stateKey = ""
	ctx.stateValue = nil
	ctx.stateSet = false
	ctx.nativeState = false
	r.rootContexts.Put(ctx)
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

// rootMiddlewareStaticLink connects middleware built in registration order without an atomic load per stage.
type rootMiddlewareStaticLink struct {
	next web.Handler
}

// handle invokes the immutable continuation assigned after middleware construction.
func (l *rootMiddlewareStaticLink) handle(ctx web.Context) error {
	return l.next(ctx)
}

// webRouteHandler lets an already-selected Echo route enter the shared root middleware chain directly.
type webRouteHandler struct {
	handler      web.Handler
	root         *routerAdapter
	routeHandler echo.HandlerFunc
}

// handle invokes root middleware inside Web-owned routes when no other Echo middleware changes ordering.
func (h *webRouteHandler) handle(c *echo.Context) error {
	if h == nil || h.root == nil || h.handler == nil {
		return echo.ErrInternalServerError
	}
	head := h.root.rootMiddleware.Load()
	if head == nil || !h.root.directWebRoutes || len(h.root.engine.Middlewares()) != 1 || len(h.root.engine.PreMiddlewares()) != 0 {
		return h.handler(acquireContextAdapter(c))
	}
	ctx := h.root.acquireRootContext(c, nil, h.handler)
	err := head.handler(ctx)
	if err != nil {
		ctx.promoteStateToNative()
	}
	h.root.releaseRootContext(ctx)
	return err
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

// newWebRouteHandler binds one adapted handler to the root middleware owner selected at request time.
func (r *routerAdapter) newWebRouteHandler(handler web.Handler) *webRouteHandler {
	adapted := &webRouteHandler{handler: handler, root: r.rootRouter()}
	adapted.routeHandler = adapted.handle
	if adapted.root.directWebRoutes && adapted.root.webRouteHandlerCode == 0 {
		adapted.root.webRouteHandlerCode = reflect.ValueOf(adapted.routeHandler).Pointer()
	}
	return adapted
}

// CONNECT registers a CONNECT route.
func (r *routerAdapter) CONNECT(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := r.newWebRouteHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))
	r.group.CONNECT(path, adapted.routeHandler)
}

// DELETE registers a DELETE route.
func (r *routerAdapter) DELETE(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := r.newWebRouteHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))
	r.group.DELETE(path, adapted.routeHandler)
}

// GET registers a GET route.
func (r *routerAdapter) GET(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := r.newWebRouteHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))
	r.group.GET(path, adapted.routeHandler)
}

// GETWS registers a GET websocket route.
func (r *routerAdapter) GETWS(path string, handler web.WebSocketHandler, middleware ...web.Middleware) {
	adapted := r.newWebRouteHandler(applyWebSocketHandler(handler, r.routeMiddlewares(middleware)...))
	r.group.GET(path, adapted.routeHandler)
}

// HEAD registers a HEAD route.
func (r *routerAdapter) HEAD(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := r.newWebRouteHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))
	r.group.HEAD(path, adapted.routeHandler)
}

// OPTIONS registers an OPTIONS route.
func (r *routerAdapter) OPTIONS(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := r.newWebRouteHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))
	r.group.OPTIONS(path, adapted.routeHandler)
}

// PATCH registers a PATCH route.
func (r *routerAdapter) PATCH(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := r.newWebRouteHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))
	r.group.PATCH(path, adapted.routeHandler)
}

// POST registers a POST route.
func (r *routerAdapter) POST(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := r.newWebRouteHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))
	r.group.POST(path, adapted.routeHandler)
}

// PUT registers a PUT route.
func (r *routerAdapter) PUT(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := r.newWebRouteHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))
	r.group.PUT(path, adapted.routeHandler)
}

// TRACE registers a TRACE route.
func (r *routerAdapter) TRACE(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := r.newWebRouteHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))
	r.group.TRACE(path, adapted.routeHandler)
}

// Any registers a route for every standard HTTP method.
func (r *routerAdapter) Any(path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := r.newWebRouteHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))
	r.group.Any(path, adapted.routeHandler)
}

// Match registers a route for the provided set of HTTP methods.
func (r *routerAdapter) Match(methods []string, path string, handler web.Handler, middleware ...web.Middleware) {
	adapted := r.newWebRouteHandler(applyMiddlewares(handler, r.routeMiddlewares(middleware)...))
	r.group.Match(methods, path, adapted.routeHandler)
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

// rootRouter returns the adapter that owns request-wide middleware.
func (r *routerAdapter) rootRouter() *routerAdapter {
	for r.parent != nil {
		r = r.parent
	}
	return r
}

// adaptRouterMiddlewares bridges web middleware into Echo middleware for a router scope.
func adaptRouterMiddlewares(r *routerAdapter) echo.MiddlewareFunc {
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
	if r.directWebRoutes && len(r.engine.Middlewares()) == 1 && len(r.engine.PreMiddlewares()) == 0 && r.webRouteHandlerCode != 0 && reflect.ValueOf(next).Pointer() == r.webRouteHandlerCode {
		return next
	}

	return func(c *echo.Context) error {
		ctx := r.acquireRootContext(c, next, nil)
		ctx.nativeState = true
		defer func() {
			ctx.promoteStateToNative()
			r.releaseRootContext(ctx)
		}()
		return head.handler(ctx)
	}
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
	tail := &rootMiddlewareLink{}
	tail.next.Store(terminal)
	var head *rootMiddlewareHandler
	var previous *rootMiddlewareStaticLink
	for _, item := range middleware {
		link := &rootMiddlewareStaticLink{}
		current := &rootMiddlewareHandler{handler: item(link.handle)}
		if head == nil {
			head = current
		} else {
			previous.next = current.handler
		}
		previous = link
	}
	if previous == nil {
		return nil, nil
	}
	previous.next = tail.handle
	return head, tail
}

// invokeRootMiddlewareNext continues through the Echo middleware and route selected for this request.
func (r *routerAdapter) invokeRootMiddlewareNext(ctx web.Context) error {
	if root, ok := ctx.(*rootMiddlewareContext); ok {
		if root == nil || root.contextAdapter == nil {
			return echo.ErrInternalServerError
		}
		if root.nextWeb != nil {
			return root.nextWeb(root)
		}
		if root.nextEcho != nil {
			root.promoteStateToNative()
			return root.nextEcho(root.contextAdapter.echoContext())
		}
	}
	return echo.ErrInternalServerError
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

// applyWebSocketHandler compiles HTTP middleware around a websocket upgrade and handler.
func applyWebSocketHandler(handler web.WebSocketHandler, middleware ...web.Middleware) web.Handler {
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
	return applied
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
		if !ok || root == nil || root.nextEcho == nil || root.contextAdapter == nil {
			return echo.ErrInternalServerError
		}
		root.promoteStateToNative()
		return root.nextEcho(root.contextAdapter.echoContext())
	}, middlewares...)

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			ctx := rootMiddlewareContext{
				contextAdapter: acquireContextAdapter(c),
				nextEcho:       next,
				nativeState:    true,
			}
			defer ctx.promoteStateToNative()
			return adapted(&ctx)
		}
	}
}
