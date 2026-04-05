package web

import "context"

// Context is the app-facing HTTP context contract.
type Context interface {
	Context() context.Context
	Method() string
	Path() string
	Param(name string) string
	Query(name string) string
	Header(name string) string
	Bind(target any) error
	Set(key string, value any)
	Get(key string) any
	SetHeader(name string, value string)
	JSON(code int, payload any) error
	Blob(code int, contentType string, body []byte) error
	File(path string) error
	Text(code int, body string) error
	HTML(code int, body string) error
	NoContent(code int) error
	Redirect(code int, url string) error
	Native() any
}
