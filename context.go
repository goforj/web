package web

import (
	"context"
	"net/http"
)

// Context is the app-facing HTTP context contract.
type Context interface {
	Context() context.Context
	Method() string
	Path() string
	URI() string
	Scheme() string
	Host() string
	Param(name string) string
	Query(name string) string
	Header(name string) string
	Cookie(name string) (*http.Cookie, error)
	RealIP() string
	Request() *http.Request
	SetRequest(request *http.Request)
	Response() Response
	ResponseWriter() http.ResponseWriter
	SetResponseWriter(writer http.ResponseWriter)
	Bind(target any) error
	Set(key string, value any)
	Get(key string) any
	AddHeader(name string, value string)
	SetHeader(name string, value string)
	SetCookie(cookie *http.Cookie)
	JSON(code int, payload any) error
	Blob(code int, contentType string, body []byte) error
	File(path string) error
	Text(code int, body string) error
	HTML(code int, body string) error
	NoContent(code int) error
	Redirect(code int, url string) error
	StatusCode() int
	Native() any
}

type contextSetter interface {
	SetContext(context.Context)
}

type rawRequester interface {
	RawRequest() *http.Request
}

// BindContext attaches ctx to the request-scoped web.Context without forcing
// callers to replace the underlying *http.Request when the adapter can carry
// an override more cheaply.
func BindContext(target Context, ctx context.Context) {
	if target == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if setter, ok := target.(contextSetter); ok {
		setter.SetContext(ctx)
		return
	}
	req := target.Request()
	if req == nil {
		return
	}
	target.SetRequest(req.WithContext(ctx))
}

// RawRequest returns the adapter's underlying request when available, without
// forcing a context-rebound clone. Callers should prefer Context() for scoped
// execution state and use RawRequest only when they need direct request body or
// header access.
func RawRequest(target Context) *http.Request {
	if target == nil {
		return nil
	}
	if requester, ok := target.(rawRequester); ok {
		return requester.RawRequest()
	}
	return target.Request()
}
