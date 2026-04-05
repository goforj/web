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
	ResponseWriter() http.ResponseWriter
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
