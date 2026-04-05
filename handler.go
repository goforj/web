package web

// Handler is the app-facing HTTP handler contract.
type Handler func(Context) error

// Middleware wraps a handler with request/response behavior.
type Middleware func(Handler) Handler
