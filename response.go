package web

import "net/http"

// Response is the app-facing HTTP response contract.
type Response interface {
	Header() http.Header
	Writer() http.ResponseWriter
	SetWriter(writer http.ResponseWriter)
	StatusCode() int
	Size() int64
	Committed() bool
	Native() any
}
