package web

// Router is the app-facing route registration contract.
type Router interface {
	Use(...Middleware)
	Get(path string, handler Handler, middleware ...Middleware)
	GetWS(path string, handler WebSocketHandler, middleware ...Middleware)
	Post(path string, handler Handler, middleware ...Middleware)
	Put(path string, handler Handler, middleware ...Middleware)
	Patch(path string, handler Handler, middleware ...Middleware)
	Delete(path string, handler Handler, middleware ...Middleware)
	Group(prefix string, middleware ...Middleware) Router
}
