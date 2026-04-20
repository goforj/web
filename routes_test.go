package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

func TestRouteAndRouteGroupHelpers(t *testing.T) {
	handlerCalled := false
	middleware := func(next Handler) Handler {
		return func(c Context) error {
			return next(c)
		}
	}
	route := NewRoute(http.MethodGet, "/healthz", func(c Context) error {
		handlerCalled = true
		return c.NoContent(http.StatusCreated)
	}, middleware)

	ctx := newRouteTestContext()
	if err := route.Handler()(ctx); err != nil {
		t.Fatalf("Handler(): %v", err)
	}
	if !handlerCalled {
		t.Fatal("handler was not called")
	}
	if got, want := ctx.StatusCode(), http.StatusCreated; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	if got := len(route.Middlewares()); got != 1 {
		t.Fatalf("len(Middlewares()) = %d", got)
	}
	emptyRoute := NewRoute(http.MethodGet, "/empty", func(c Context) error { return nil })
	if got := len(emptyRoute.Middlewares()); got != 0 {
		t.Fatalf("empty route middlewares = %d", got)
	}

	group := NewRouteGroup("/api", []Route{route}, middleware).WithMiddlewareNames("auth")
	if got, want := group.RoutePrefix(), "/api"; got != want {
		t.Fatalf("RoutePrefix() = %q, want %q", got, want)
	}
	if got := len(group.Routes()); got != 1 {
		t.Fatalf("len(Routes()) = %d", got)
	}
	if got := len(group.Middlewares()); got != 1 {
		t.Fatalf("len(group.Middlewares()) = %d", got)
	}
	if got, want := group.MiddlewareNames()[0], "auth"; got != want {
		t.Fatalf("MiddlewareNames()[0] = %q, want %q", got, want)
	}
}

func TestMountRouterSkipsNilAndReturnsErrors(t *testing.T) {
	var mounted bool
	wantErr := errors.New("mount failed")
	router := &stubRouter{}
	err := MountRouter(router, []RouterMount{
		nil,
		func(r Router) error {
			mounted = true
			r.GET("/healthz", func(c Context) error { return nil })
			return nil
		},
		func(r Router) error {
			return wantErr
		},
	})

	if !mounted {
		t.Fatal("expected first mount to run")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("MountRouter() error = %v", err)
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

type routeTestContext struct {
	recorder *httptest.ResponseRecorder
	response routeTestResponse
}

func newRouteTestContext() *routeTestContext {
	ctx := &routeTestContext{recorder: httptest.NewRecorder()}
	ctx.response.context = ctx
	return ctx
}

func (c *routeTestContext) Context() context.Context              { return context.Background() }
func (c *routeTestContext) Method() string                        { return http.MethodGet }
func (c *routeTestContext) Path() string                          { return "/healthz" }
func (c *routeTestContext) URI() string                           { return "/healthz" }
func (c *routeTestContext) Scheme() string                        { return "http" }
func (c *routeTestContext) Host() string                          { return "example.com" }
func (c *routeTestContext) Param(string) string                   { return "" }
func (c *routeTestContext) Query(string) string                   { return "" }
func (c *routeTestContext) Header(string) string                  { return "" }
func (c *routeTestContext) Cookie(string) (*http.Cookie, error)   { return nil, http.ErrNoCookie }
func (c *routeTestContext) RealIP() string                        { return "127.0.0.1" }
func (c *routeTestContext) Request() *http.Request                { return httptest.NewRequest(http.MethodGet, "/healthz", nil) }
func (c *routeTestContext) SetRequest(*http.Request)              {}
func (c *routeTestContext) Response() Response                    { return &c.response }
func (c *routeTestContext) ResponseWriter() http.ResponseWriter   { return c.recorder }
func (c *routeTestContext) SetResponseWriter(http.ResponseWriter) {}
func (c *routeTestContext) Bind(any) error                        { return nil }
func (c *routeTestContext) Set(string, any)                       {}
func (c *routeTestContext) Get(string) any                        { return nil }
func (c *routeTestContext) AddHeader(name, value string)          { c.recorder.Header().Add(name, value) }
func (c *routeTestContext) SetHeader(name, value string)          { c.recorder.Header().Set(name, value) }
func (c *routeTestContext) SetCookie(cookie *http.Cookie)         { http.SetCookie(c.recorder, cookie) }
func (c *routeTestContext) JSON(code int, payload any) error      { c.recorder.WriteHeader(code); return nil }
func (c *routeTestContext) Blob(code int, contentType string, body []byte) error {
	c.recorder.WriteHeader(code)
	_, err := c.recorder.Write(body)
	return err
}
func (c *routeTestContext) File(string) error                     { return nil }
func (c *routeTestContext) Text(code int, body string) error      { c.recorder.WriteHeader(code); _, err := c.recorder.WriteString(body); return err }
func (c *routeTestContext) HTML(code int, body string) error      { c.recorder.WriteHeader(code); _, err := c.recorder.WriteString(body); return err }
func (c *routeTestContext) NoContent(code int) error              { c.recorder.WriteHeader(code); return nil }
func (c *routeTestContext) Redirect(code int, url string) error   { http.Redirect(c.recorder, httptest.NewRequest(http.MethodGet, "/", nil), url, code); return nil }
func (c *routeTestContext) StatusCode() int                       { return c.recorder.Code }
func (c *routeTestContext) Native() any                           { return c.recorder }

type routeTestResponse struct {
	context *routeTestContext
}

func (r *routeTestResponse) Header() http.Header                  { return r.context.recorder.Header() }
func (r *routeTestResponse) Writer() http.ResponseWriter          { return r.context.recorder }
func (r *routeTestResponse) SetWriter(http.ResponseWriter)        {}
func (r *routeTestResponse) StatusCode() int                      { return r.context.recorder.Code }
func (r *routeTestResponse) Size() int64                          { return int64(r.context.recorder.Body.Len()) }
func (r *routeTestResponse) Committed() bool                      { return r.context.recorder.Code != 0 }
func (r *routeTestResponse) Native() any                          { return r.context.recorder }
