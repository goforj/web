package web

import "testing"

func TestRegisterRoutesRegistersWebSocketRoutes(t *testing.T) {
	routes := []string{}
	router := &stubRouter{wsRoutes: &routes}
	err := RegisterRoutes(router, []RouteGroup{
		NewRouteGroup("/api", []Route{
			NewWebSocketRoute("/stream", testWebSocketRouteHandler),
		}),
	})
	if err != nil {
		t.Fatalf("register routes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 websocket route, got %d", len(routes))
	}
	if got := routes[0]; got != "/api/stream" {
		t.Fatalf("expected websocket route /api/stream, got %q", got)
	}
}

type stubRouter struct {
	prefix   string
	wsRoutes *[]string
}

func (r *stubRouter) Pre(...Middleware) {}

func (r *stubRouter) Use(...Middleware) {}

func (r *stubRouter) Handle(method string, path string, handler Handler, middleware ...Middleware) error {
	return nil
}

func (r *stubRouter) CONNECT(path string, handler Handler, middleware ...Middleware) {}
func (r *stubRouter) DELETE(path string, handler Handler, middleware ...Middleware)  {}
func (r *stubRouter) GET(path string, handler Handler, middleware ...Middleware)     {}
func (r *stubRouter) HEAD(path string, handler Handler, middleware ...Middleware)    {}
func (r *stubRouter) OPTIONS(path string, handler Handler, middleware ...Middleware) {}
func (r *stubRouter) PATCH(path string, handler Handler, middleware ...Middleware)   {}
func (r *stubRouter) POST(path string, handler Handler, middleware ...Middleware)    {}
func (r *stubRouter) PUT(path string, handler Handler, middleware ...Middleware)     {}
func (r *stubRouter) TRACE(path string, handler Handler, middleware ...Middleware)   {}
func (r *stubRouter) Any(path string, handler Handler, middleware ...Middleware)     {}
func (r *stubRouter) Match(methods []string, path string, handler Handler, middleware ...Middleware) {
}

func (r *stubRouter) GETWS(path string, handler WebSocketHandler, middleware ...Middleware) {
	*r.wsRoutes = append(*r.wsRoutes, r.prefix+path)
}

func (r *stubRouter) Group(prefix string, middleware ...Middleware) Router {
	return &stubRouter{
		prefix:   r.prefix + prefix,
		wsRoutes: r.wsRoutes,
	}
}
