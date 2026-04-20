package web

import (
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
)

// NewRoute creates a new route using the app-facing web handler contract directly.
// @group Routing
// Example:
// route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error {
// 	return c.NoContent(http.StatusOK)
// })
// fmt.Println(route.Method(), route.Path())
//	// GET /healthz
func NewRoute(
	method string,
	route string,
	handler Handler,
	middlewares ...Middleware,
) Route {
	return Route{
		method:      method,
		route:       route,
		handler:     handler,
		handlerName: qualifyHandler(runtime.FuncForPC(reflect.ValueOf(handler).Pointer()).Name()),
		middlewares: middlewares,
	}
}

// NewWebSocketRoute creates a websocket route using the app-facing websocket handler contract.
// @group Routing
// Example:
// route := web.NewWebSocketRoute("/ws", func(c web.Context, conn web.WebSocketConn) error {
// 	return nil
// })
// fmt.Println(route.IsWebSocket())
//	// true
func NewWebSocketRoute(
	route string,
	handler WebSocketHandler,
	middlewares ...Middleware,
) Route {
	return Route{
		method:      "GETWS",
		route:       route,
		wsHandler:   handler,
		handlerName: qualifyHandler(runtime.FuncForPC(reflect.ValueOf(handler).Pointer()).Name()),
		middlewares: middlewares,
	}
}

// Route represents a single route in the application.
type Route struct {
	method          string
	route           string
	handler         Handler
	wsHandler       WebSocketHandler
	handlerName     string
	middlewares     []Middleware
	middlewareNames []string
}

// Method returns the HTTP method.
// @group Routing
// Example:
// route := web.NewRoute(http.MethodPost, "/users", func(c web.Context) error { return nil })
// fmt.Println(route.Method())
//	// POST
func (r *Route) Method() string {
	return r.method
}

// Path returns the path of the route.
// @group Routing
// Example:
// route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil })
// fmt.Println(route.Path())
//	// /healthz
func (r *Route) Path() string {
	return r.route
}

// Handler returns the route handler.
// @group Routing
// Example:
// route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil })
// _ = route.Handler()
func (r *Route) Handler() Handler {
	return r.handler
}

// WebSocketHandler returns the websocket route handler.
// @group Routing
// Example:
// route := web.NewWebSocketRoute("/ws", func(c web.Context, conn web.WebSocketConn) error { return nil })
// _ = route.WebSocketHandler()
func (r *Route) WebSocketHandler() WebSocketHandler {
	return r.wsHandler
}

// HandlerName returns the original handler name for route reporting.
// @group Routing
// Example:
// route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil })
// fmt.Println(route.HandlerName() != "")
//	// true
func (r *Route) HandlerName() string {
	return r.handlerName
}

// Middlewares returns the route middleware slice.
// @group Routing
// Example:
// route := web.NewRoute(
// 	http.MethodGet,
// 	"/healthz",
// 	func(c web.Context) error { return nil },
// 	func(next web.Handler) web.Handler { return next },
// )
// fmt.Println(len(route.Middlewares()))
//	// 1
func (r *Route) Middlewares() []Middleware {
	if len(r.middlewares) > 0 {
		return r.middlewares
	}
	return []Middleware{}
}

// MiddlewareNames returns original middleware names for route reporting.
// @group Routing
// Example:
// route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }).WithMiddlewareNames("auth")
// fmt.Println(route.MiddlewareNames()[0])
//	// auth
func (r *Route) MiddlewareNames() []string {
	return r.middlewareNames
}

// WithMiddlewareNames attaches reporting-only middleware names to the route.
// @group Routing
// Example:
// route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }).WithMiddlewareNames("auth", "trace")
// fmt.Println(len(route.MiddlewareNames()))
//	// 2
func (r Route) WithMiddlewareNames(names ...string) Route {
	r.middlewareNames = append([]string(nil), names...)
	return r
}

// IsWebSocket reports whether this route upgrades to a websocket connection.
// @group Routing
// Example:
// route := web.NewWebSocketRoute("/ws", func(c web.Context, conn web.WebSocketConn) error { return nil })
// fmt.Println(route.IsWebSocket())
//	// true
func (r *Route) IsWebSocket() bool {
	return r != nil && r.wsHandler != nil
}

// NewRouteGroup wraps routes and their accompanied web middleware.
// @group Routing
// Example:
// group := web.NewRouteGroup("/api", []web.Route{
// 	web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }),
// })
// fmt.Println(group.RoutePrefix(), len(group.Routes()))
//	// /api 1
func NewRouteGroup(
	prefix string,
	routes []Route,
	middlewares ...Middleware,
) RouteGroup {
	return RouteGroup{
		routePrefix: prefix,
		routes:      routes,
		middlewares: middlewares,
	}
}

// RouteGroup represents a group of routes.
type RouteGroup struct {
	routePrefix     string
	routes          []Route
	middlewares     []Middleware
	middlewareNames []string
}

// RoutePrefix returns the group prefix.
// @group Routing
// Example:
// group := web.NewRouteGroup("/api", nil)
// fmt.Println(group.RoutePrefix())
//	// /api
func (g *RouteGroup) RoutePrefix() string {
	return g.routePrefix
}

// Routes returns the routes in the group.
// @group Routing
// Example:
// group := web.NewRouteGroup("/api", []web.Route{
// 	web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }),
// })
// fmt.Println(len(group.Routes()))
//	// 1
func (g *RouteGroup) Routes() []Route {
	return g.routes
}

// Middlewares returns the middleware slice for the group.
// @group Routing
// Example:
// group := web.NewRouteGroup("/api", nil, func(next web.Handler) web.Handler { return next })
// fmt.Println(len(group.Middlewares()))
//	// 1
func (g *RouteGroup) Middlewares() []Middleware {
	return g.middlewares
}

// MiddlewareNames returns original middleware names for route reporting.
// @group Routing
// Example:
// group := web.NewRouteGroup("/api", nil).WithMiddlewareNames("auth")
// fmt.Println(group.MiddlewareNames()[0])
//	// auth
func (g *RouteGroup) MiddlewareNames() []string {
	return g.middlewareNames
}

// WithMiddlewareNames attaches reporting-only middleware names to the group.
// @group Routing
// Example:
// group := web.NewRouteGroup("/api", nil).WithMiddlewareNames("auth", "trace")
// fmt.Println(len(group.MiddlewareNames()))
//	// 2
func (g RouteGroup) WithMiddlewareNames(names ...string) RouteGroup {
	g.middlewareNames = append([]string(nil), names...)
	return g
}

// RegisterRoutes registers route groups onto a router.
// @group Routing
// Example:
// adapter := echoweb.New()
// groups := []web.RouteGroup{
// 	web.NewRouteGroup("/api", []web.Route{
// 		web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }),
// 	}),
// }
// err := web.RegisterRoutes(adapter.Router(), groups)
// fmt.Println(err == nil)
//	// true
func RegisterRoutes(router Router, groups []RouteGroup) error {
	for _, group := range groups {
		g := router.Group(group.RoutePrefix(), group.Middlewares()...)
		for _, route := range group.Routes() {
			if route.IsWebSocket() {
				g.GETWS(route.Path(), route.WebSocketHandler(), route.Middlewares()...)
				continue
			}
			if err := g.Handle(route.Method(), route.Path(), route.Handler(), route.Middlewares()...); err != nil {
				return err
			}
		}
	}
	return nil
}

// MountRouter applies mount-style router configuration in declaration order.
// @group Routing
// Example:
// adapter := echoweb.New()
// err := web.MountRouter(adapter.Router(), []web.RouterMount{
// 	func(r web.Router) error {
// 		r.GET("/healthz", func(c web.Context) error { return nil })
// 		return nil
// 	},
// })
// fmt.Println(err == nil)
//	// true
func MountRouter(router Router, mounts []RouterMount) error {
	for _, mount := range mounts {
		if mount == nil {
			continue
		}
		if err := mount(router); err != nil {
			return err
		}
	}
	return nil
}

// qualifyHandler normalizes a runtime function name into a compact,
// console-friendly identifier like "monitoring.Summary".
//
// runtime.FuncForPC names are verbose and inconsistent across:
// - method values, which end in "-fm"
// - compiler-generated closures, which end in ".funcN"
// - pointer receiver methods, which include "(*Type)"
//
// The route list only needs a stable, human-readable package-ish prefix plus
// the method/function name, not the full runtime symbol.
func qualifyHandler(name string) string {
	safe := filepath.ToSlash(name)
	safe = strings.TrimSuffix(safe, "-fm")
	safe = regexp.MustCompile(`\.func\d+$`).ReplaceAllString(safe, "")

	lastDot := strings.LastIndex(safe, ".")
	if lastDot == -1 {
		return "handler.unknown"
	}
	method := safe[lastDot+1:]
	beforeMethod := safe[:lastDot]
	// Drop pointer receiver noise so methods and free functions format the same.
	beforeMethod = regexp.MustCompile(`\(\*[^)]+\)`).ReplaceAllString(beforeMethod, "")
	beforeMethod = strings.TrimSuffix(beforeMethod, ".")

	parts := strings.Split(beforeMethod, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		pkg := strings.Trim(parts[i], ".")
		// Skip generic path segments so we surface the app/domain package name
		// instead of broad framework buckets like "internal" or "http".
		if !isGenericPackage(pkg) && pkg != "" {
			return fmt.Sprintf("%s.%s", pkg, method)
		}
	}
	return fmt.Sprintf("handler.%s", method)
}

func isGenericPackage(pkg string) bool {
	switch pkg {
	case "internal", "http", "controllers", "handlers":
		return true
	default:
		return false
	}
}
