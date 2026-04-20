package webtest

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/goforj/web"
)

// PathParams captures named path parameter values for test contexts.
type PathParams map[string]string

// Context is a lightweight web.Context implementation for handler tests built on httptest.
type Context struct {
	request    *http.Request
	recorder   *httptest.ResponseRecorder
	response   Response
	path       string
	pathParams PathParams
	values     map[string]any
}

var _ web.Context = (*Context)(nil)

// NewContext creates a new test context around the provided request/recorder pair.
// @group Testing
// Example:
// req := httptest.NewRequest(http.MethodGet, "/users/42?expand=roles", nil)
// ctx := webtest.NewContext(req, nil, "/users/:id", webtest.PathParams{"id": "42"})
// fmt.Println(ctx.Param("id"), ctx.Query("expand"))
//	// 42 roles
func NewContext(request *http.Request, recorder *httptest.ResponseRecorder, path string, pathParams PathParams) *Context {
	if request == nil {
		request = httptest.NewRequest(http.MethodGet, "/", nil)
	}
	if recorder == nil {
		recorder = httptest.NewRecorder()
	}
	if pathParams == nil {
		pathParams = PathParams{}
	}
	ctx := &Context{
		request:    request,
		recorder:   recorder,
		path:       path,
		pathParams: pathParams,
		values:     map[string]any{},
	}
	ctx.response.context = ctx
	return ctx
}

func (c *Context) Context() context.Context {
	return c.request.Context()
}

func (c *Context) Method() string {
	return c.request.Method
}

func (c *Context) Path() string {
	return c.path
}

func (c *Context) URI() string {
	if c.request.URL == nil {
		return ""
	}
	return c.request.URL.RequestURI()
}

func (c *Context) Scheme() string {
	if c.request.TLS != nil {
		return "https"
	}
	if forwarded := c.request.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		return forwarded
	}
	return "http"
}

func (c *Context) Host() string {
	return c.request.Host
}

func (c *Context) Param(name string) string {
	return c.pathParams[name]
}

func (c *Context) Query(name string) string {
	return c.request.URL.Query().Get(name)
}

func (c *Context) Header(name string) string {
	return c.request.Header.Get(name)
}

func (c *Context) Cookie(name string) (*http.Cookie, error) {
	return c.request.Cookie(name)
}

func (c *Context) RealIP() string {
	for _, name := range []string{"X-Forwarded-For", "X-Real-IP"} {
		if raw := c.request.Header.Get(name); raw != "" {
			if idx := strings.IndexByte(raw, ','); idx >= 0 {
				return strings.TrimSpace(raw[:idx])
			}
			return strings.TrimSpace(raw)
		}
	}
	host, _, err := net.SplitHostPort(c.request.RemoteAddr)
	if err == nil {
		return host
	}
	return c.request.RemoteAddr
}

func (c *Context) Request() *http.Request {
	return c.request
}

func (c *Context) SetRequest(request *http.Request) {
	c.request = request
}

func (c *Context) Response() web.Response {
	return &c.response
}

func (c *Context) ResponseWriter() http.ResponseWriter {
	return c.recorder
}

func (c *Context) SetResponseWriter(writer http.ResponseWriter) {
	if recorder, ok := writer.(*httptest.ResponseRecorder); ok {
		c.recorder = recorder
		return
	}
	panic("webtest: response writer must be *httptest.ResponseRecorder")
}

func (c *Context) Bind(target any) error {
	return json.NewDecoder(c.request.Body).Decode(target)
}

func (c *Context) Set(key string, value any) {
	c.values[key] = value
}

func (c *Context) Get(key string) any {
	return c.values[key]
}

func (c *Context) AddHeader(name string, value string) {
	c.recorder.Header().Add(name, value)
}

func (c *Context) SetHeader(name string, value string) {
	c.recorder.Header().Set(name, value)
}

func (c *Context) SetCookie(cookie *http.Cookie) {
	http.SetCookie(c.recorder, cookie)
}

func (c *Context) JSON(code int, payload any) error {
	c.recorder.Header().Set("Content-Type", "application/json; charset=UTF-8")
	c.recorder.WriteHeader(code)
	return json.NewEncoder(c.recorder).Encode(payload)
}

func (c *Context) Blob(code int, contentType string, body []byte) error {
	c.recorder.Header().Set("Content-Type", contentType)
	c.recorder.WriteHeader(code)
	_, err := c.recorder.Write(body)
	return err
}

func (c *Context) File(path string) error {
	http.ServeFile(c.recorder, c.request, path)
	return nil
}

func (c *Context) Text(code int, body string) error {
	c.recorder.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	c.recorder.WriteHeader(code)
	_, err := c.recorder.WriteString(body)
	return err
}

func (c *Context) HTML(code int, body string) error {
	c.recorder.Header().Set("Content-Type", "text/html; charset=UTF-8")
	c.recorder.WriteHeader(code)
	_, err := c.recorder.WriteString(body)
	return err
}

func (c *Context) NoContent(code int) error {
	c.recorder.WriteHeader(code)
	return nil
}

func (c *Context) Redirect(code int, url string) error {
	http.Redirect(c.recorder, c.request, url, code)
	return nil
}

func (c *Context) StatusCode() int {
	return c.response.StatusCode()
}

func (c *Context) Native() any {
	return c.recorder
}

type Response struct {
	context *Context
}

var _ web.Response = (*Response)(nil)

func (r *Response) Header() http.Header {
	return r.context.recorder.Header()
}

func (r *Response) Writer() http.ResponseWriter {
	return r.context.recorder
}

func (r *Response) SetWriter(writer http.ResponseWriter) {
	r.context.SetResponseWriter(writer)
}

func (r *Response) StatusCode() int {
	return r.context.recorder.Code
}

func (r *Response) Size() int64 {
	return int64(r.context.recorder.Body.Len())
}

func (r *Response) Committed() bool {
	return r.context.recorder.Result() != nil && (r.context.recorder.Code != 0 || r.context.recorder.Body.Len() > 0)
}

func (r *Response) Native() any {
	return r.context.recorder
}
