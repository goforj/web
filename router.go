package web

// Router is the app-facing route registration contract.
type Router interface {
	Pre(...Middleware)
	Use(...Middleware)
	Handle(method string, path string, handler Handler, middleware ...Middleware) error
	CONNECT(path string, handler Handler, middleware ...Middleware)
	DELETE(path string, handler Handler, middleware ...Middleware)
	GET(path string, handler Handler, middleware ...Middleware)
	GETWS(path string, handler WebSocketHandler, middleware ...Middleware)
	HEAD(path string, handler Handler, middleware ...Middleware)
	OPTIONS(path string, handler Handler, middleware ...Middleware)
	PATCH(path string, handler Handler, middleware ...Middleware)
	POST(path string, handler Handler, middleware ...Middleware)
	PUT(path string, handler Handler, middleware ...Middleware)
	TRACE(path string, handler Handler, middleware ...Middleware)
	Any(path string, handler Handler, middleware ...Middleware)
	Match(methods []string, path string, handler Handler, middleware ...Middleware)
	Group(prefix string, middleware ...Middleware) Router
}
