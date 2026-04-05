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

// Route represents a single route in the application.
type Route struct {
	method          string
	route           string
	handler         Handler
	handlerName     string
	middlewares     []Middleware
	middlewareNames []string
}

// Method returns the HTTP method.
func (r *Route) Method() string {
	return r.method
}

// Path returns the path of the route.
func (r *Route) Path() string {
	return r.route
}

// Handler returns the route handler.
func (r *Route) Handler() Handler {
	return r.handler
}

// HandlerName returns the original handler name for route reporting.
func (r *Route) HandlerName() string {
	return r.handlerName
}

// Middlewares returns the route middleware slice.
func (r *Route) Middlewares() []Middleware {
	if len(r.middlewares) > 0 {
		return r.middlewares
	}
	return []Middleware{}
}

// MiddlewareNames returns original middleware names for route reporting.
func (r *Route) MiddlewareNames() []string {
	return r.middlewareNames
}

// WithMiddlewareNames attaches reporting-only middleware names to the route.
func (r Route) WithMiddlewareNames(names ...string) Route {
	r.middlewareNames = append([]string(nil), names...)
	return r
}

// NewRouteGroup wraps routes and their accompanied web middleware.
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
func (g *RouteGroup) RoutePrefix() string {
	return g.routePrefix
}

// Routes returns the routes in the group.
func (g *RouteGroup) Routes() []Route {
	return g.routes
}

// Middlewares returns the middleware slice for the group.
func (g *RouteGroup) Middlewares() []Middleware {
	return g.middlewares
}

// MiddlewareNames returns original middleware names for route reporting.
func (g *RouteGroup) MiddlewareNames() []string {
	return g.middlewareNames
}

// WithMiddlewareNames attaches reporting-only middleware names to the group.
func (g RouteGroup) WithMiddlewareNames(names ...string) RouteGroup {
	g.middlewareNames = append([]string(nil), names...)
	return g
}

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
	beforeMethod = regexp.MustCompile(`\(\*[^)]+\)`).ReplaceAllString(beforeMethod, "")
	beforeMethod = strings.TrimSuffix(beforeMethod, ".")

	parts := strings.Split(beforeMethod, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		pkg := strings.Trim(parts[i], ".")
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
