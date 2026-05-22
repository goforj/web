package echoweb

import (
	"context"
	"net/http"
	"sync"

	"github.com/goforj/web"
	echo "github.com/labstack/echo/v5"
)

type contextAdapter struct {
	echo           *echo.Context
	echoResponse   *echo.Response
	responseWriter http.ResponseWriter
	context        context.Context
	request        *http.Request
	sourceName     string
	sourceContext  context.Context
	reusable       bool
	response       responseAdapter
}

var _ web.Context = (*contextAdapter)(nil)

var contextAdapterPool = sync.Pool{
	New: func() any {
		adapted := &contextAdapter{}
		adapted.response.context = adapted
		return adapted
	},
}

// acquireContextAdapter prepares a pooled adapter for one Echo request.
func acquireContextAdapter(c *echo.Context) *contextAdapter {
	// The adapter is pooled because every request needs one, but the embedded
	// Echo pointers and cached derived state must be reset between requests.
	adapted := contextAdapterPool.Get().(*contextAdapter)
	adapted.echo = c
	adapted.reusable = true
	adapted.echoResponse = nil
	adapted.responseWriter = nil
	adapted.context = nil
	adapted.request = nil
	adapted.sourceName = ""
	adapted.sourceContext = nil
	return adapted
}

// releaseContextAdapter returns a reusable adapter to the pool after a request finishes.
func releaseContextAdapter(adapted *contextAdapter) {
	if adapted == nil || !adapted.reusable {
		return
	}
	adapted.echo = nil
	adapted.echoResponse = nil
	adapted.responseWriter = nil
	adapted.context = nil
	adapted.request = nil
	adapted.sourceName = ""
	adapted.sourceContext = nil
	contextAdapterPool.Put(adapted)
}

// Context returns the effective request context, including any source-only fast-path state.
func (c *contextAdapter) Context() context.Context {
	if c.context != nil {
		return c.context
	}
	base := c.echo.Request().Context()
	if c.sourceName == "" {
		return base
	}
	if c.sourceContext == nil {
		// Source-only propagation is common on the hot path, so keep it as a
		// lightweight wrapper instead of forcing a request clone.
		c.sourceContext = sourceNameContext{
			Context: base,
			source:  c.sourceName,
		}
	}
	return c.sourceContext
}

// Method returns the current request method.
func (c *contextAdapter) Method() string {
	return c.echo.Request().Method
}

// Path returns the matched route path.
func (c *contextAdapter) Path() string {
	return c.echo.Path()
}

// URI returns the current request URI.
func (c *contextAdapter) URI() string {
	return c.echo.Request().URL.RequestURI()
}

// Scheme returns the resolved request scheme.
func (c *contextAdapter) Scheme() string {
	return c.echo.Scheme()
}

// Host returns the request host.
func (c *contextAdapter) Host() string {
	return c.echo.Request().Host
}

// Param returns a named route parameter.
func (c *contextAdapter) Param(name string) string {
	return c.echo.Param(name)
}

// Query returns a query parameter value.
func (c *contextAdapter) Query(name string) string {
	return c.echo.QueryParam(name)
}

// Header returns a request header value.
func (c *contextAdapter) Header(name string) string {
	return c.echo.Request().Header.Get(name)
}

// Cookie returns a named request cookie.
func (c *contextAdapter) Cookie(name string) (*http.Cookie, error) {
	return c.echo.Cookie(name)
}

// RealIP returns the best-effort client IP address.
func (c *contextAdapter) RealIP() string {
	return c.echo.RealIP()
}

// Request returns the request, rebinding its context only when needed.
func (c *contextAdapter) Request() *http.Request {
	base := c.echo.Request()
	if c.context == nil || base == nil {
		return base
	}
	if c.request == nil || c.request.Context() != c.context {
		// Only materialize a rebound request when a caller explicitly asks for it.
		c.request = base.WithContext(c.context)
	}
	return c.request
}

// RawRequest returns the underlying Echo request without rebinding.
func (c *contextAdapter) RawRequest() *http.Request {
	if c == nil || c.echo == nil {
		return nil
	}
	return c.echo.Request()
}

// SetRequest replaces the underlying request.
func (c *contextAdapter) SetRequest(request *http.Request) {
	c.echo.SetRequest(request)
	c.request = nil
}

// SetContext overrides the effective request context for subsequent Request and Context calls.
func (c *contextAdapter) SetContext(ctx context.Context) {
	// Any explicit context override supersedes the cheaper source-name fast path.
	c.context = ctx
	c.request = nil
	c.sourceName = ""
	c.sourceContext = nil
}

// SetAppSourceName records the application source name on the adapter fast path.
func (c *contextAdapter) SetAppSourceName(source string) {
	c.sourceName = source
	c.sourceContext = nil
}

// AppSourceName returns the application source name attached to the request.
func (c *contextAdapter) AppSourceName() string {
	return c.sourceName
}

// Response returns the response adapter for the current request.
func (c *contextAdapter) Response() web.Response {
	return &c.response
}

// ResponseWriter returns the active response writer.
func (c *contextAdapter) ResponseWriter() http.ResponseWriter {
	return c.echo.Response()
}

// SetResponseWriter replaces the active response writer.
func (c *contextAdapter) SetResponseWriter(writer http.ResponseWriter) {
	c.echo.SetResponse(writer)
	c.refreshResponse()
}

// Bind decodes the request body into target using Echo's binder.
func (c *contextAdapter) Bind(target any) error {
	return c.echo.Bind(target)
}

// Set stores request-scoped data on the underlying Echo context.
func (c *contextAdapter) Set(key string, value any) {
	c.echo.Set(key, value)
}

// Get reads request-scoped data from the underlying Echo context.
func (c *contextAdapter) Get(key string) any {
	return c.echo.Get(key)
}

// AddHeader appends a response header value.
func (c *contextAdapter) AddHeader(name string, value string) {
	c.echo.Response().Header().Add(name, value)
}

// SetHeader sets a response header value.
func (c *contextAdapter) SetHeader(name string, value string) {
	c.echo.Response().Header().Set(name, value)
}

// SetCookie writes a response cookie.
func (c *contextAdapter) SetCookie(cookie *http.Cookie) {
	http.SetCookie(c.echo.Response(), cookie)
}

// JSON writes a JSON response.
func (c *contextAdapter) JSON(code int, payload any) error {
	return c.echo.JSON(code, payload)
}

// Blob writes a raw byte response with the provided content type.
func (c *contextAdapter) Blob(code int, contentType string, body []byte) error {
	return c.echo.Blob(code, contentType, body)
}

// File serves a file from disk.
func (c *contextAdapter) File(path string) error {
	http.ServeFile(c.echo.Response(), c.echo.Request(), path)
	return nil
}

// Text writes a plain-text response.
func (c *contextAdapter) Text(code int, body string) error {
	return c.echo.String(code, body)
}

// HTML writes an HTML response.
func (c *contextAdapter) HTML(code int, body string) error {
	return c.echo.HTML(code, body)
}

// NoContent writes an empty response with the provided status code.
func (c *contextAdapter) NoContent(code int) error {
	return c.echo.NoContent(code)
}

// Redirect sends an HTTP redirect response.
func (c *contextAdapter) Redirect(code int, url string) error {
	return c.echo.Redirect(code, url)
}

// StatusCode returns the response status code.
func (c *contextAdapter) StatusCode() int {
	return c.response.StatusCode()
}

// Native returns the underlying Echo context.
func (c *contextAdapter) Native() any {
	return c.echo
}

// DisableReuse prevents the adapter from returning to the pool after use.
func (c *contextAdapter) DisableReuse() {
	c.reusable = false
}

// refreshResponse resynchronizes cached response wrappers after writer swaps.
func (c *contextAdapter) refreshResponse() {
	if c == nil || c.echo == nil {
		c.echoResponse = nil
		c.responseWriter = nil
		return
	}
	writer := c.echo.Response()
	c.responseWriter = writer
	response, err := echo.UnwrapResponse(writer)
	if err != nil {
		c.echoResponse = nil
		return
	}
	c.echoResponse = response
}

type responseAdapter struct {
	context *contextAdapter
}

type sourceNameContext struct {
	context.Context
	source string
}

// AppSourceName returns the app source name carried by this lightweight context wrapper.
func (c sourceNameContext) AppSourceName() string {
	return c.source
}

var _ web.Response = (*responseAdapter)(nil)

// Header returns the response header map.
func (r *responseAdapter) Header() http.Header {
	return r.context.echo.Response().Header()
}

// Writer returns the active response writer.
func (r *responseAdapter) Writer() http.ResponseWriter {
	return r.context.echo.Response()
}

// SetWriter replaces the active response writer.
func (r *responseAdapter) SetWriter(writer http.ResponseWriter) {
	r.context.echo.SetResponse(writer)
	r.context.refreshResponse()
}

// StatusCode returns the committed response status code.
func (r *responseAdapter) StatusCode() int {
	response := r.currentResponse()
	if response == nil {
		return 0
	}
	return response.Status
}

// Size returns the number of bytes written to the response.
func (r *responseAdapter) Size() int64 {
	response := r.currentResponse()
	if response == nil {
		return 0
	}
	return response.Size
}

// Committed reports whether the response has been committed.
func (r *responseAdapter) Committed() bool {
	response := r.currentResponse()
	if response == nil {
		return false
	}
	return response.Committed
}

// Native returns the underlying Echo response writer.
func (r *responseAdapter) Native() any {
	return r.context.echo.Response()
}

// currentResponse returns the cached Echo response, refreshing it when needed.
func (r *responseAdapter) currentResponse() *echo.Response {
	if r == nil || r.context == nil {
		return nil
	}
	if r.context.responseWriter != r.context.echo.Response() {
		r.context.refreshResponse()
	}
	return r.context.echoResponse
}

// UnwrapContext returns the underlying Echo context when the web.Context came from this adapter.
// @group Adapter
// Example:
// adapter := echoweb.New()
// adapter.Router().GET("/healthz", func(c web.Context) error {
// 	_, ok := echoweb.UnwrapContext(c)
// 	fmt.Println(ok)
// 	return c.NoContent(http.StatusOK)
// })
// rr := httptest.NewRecorder()
// req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
// adapter.ServeHTTP(rr, req)
//	// true
func UnwrapContext(ctx web.Context) (*echo.Context, bool) {
	adapted, ok := ctx.(*contextAdapter)
	if !ok || adapted == nil || adapted.echo == nil {
		return nil, false
	}
	return adapted.echo, true
}
