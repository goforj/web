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

func acquireContextAdapter(c *echo.Context) *contextAdapter {
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

func (c *contextAdapter) Context() context.Context {
	if c.context != nil {
		return c.context
	}
	base := c.echo.Request().Context()
	if c.sourceName == "" {
		return base
	}
	if c.sourceContext == nil {
		c.sourceContext = sourceNameContext{
			Context: base,
			source:  c.sourceName,
		}
	}
	return c.sourceContext
}

func (c *contextAdapter) Method() string {
	return c.echo.Request().Method
}

func (c *contextAdapter) Path() string {
	return c.echo.Path()
}

func (c *contextAdapter) URI() string {
	return c.echo.Request().URL.RequestURI()
}

func (c *contextAdapter) Scheme() string {
	return c.echo.Scheme()
}

func (c *contextAdapter) Host() string {
	return c.echo.Request().Host
}

func (c *contextAdapter) Param(name string) string {
	return c.echo.Param(name)
}

func (c *contextAdapter) Query(name string) string {
	return c.echo.QueryParam(name)
}

func (c *contextAdapter) Header(name string) string {
	return c.echo.Request().Header.Get(name)
}

func (c *contextAdapter) Cookie(name string) (*http.Cookie, error) {
	return c.echo.Cookie(name)
}

func (c *contextAdapter) RealIP() string {
	return c.echo.RealIP()
}

func (c *contextAdapter) Request() *http.Request {
	base := c.echo.Request()
	if c.context == nil || base == nil {
		return base
	}
	if c.request == nil || c.request.Context() != c.context {
		c.request = base.WithContext(c.context)
	}
	return c.request
}

func (c *contextAdapter) RawRequest() *http.Request {
	if c == nil || c.echo == nil {
		return nil
	}
	return c.echo.Request()
}

func (c *contextAdapter) SetRequest(request *http.Request) {
	c.echo.SetRequest(request)
	c.request = nil
}

func (c *contextAdapter) SetContext(ctx context.Context) {
	c.context = ctx
	c.request = nil
	c.sourceName = ""
	c.sourceContext = nil
}

func (c *contextAdapter) SetAppSourceName(source string) {
	c.sourceName = source
	c.sourceContext = nil
}

func (c *contextAdapter) AppSourceName() string {
	return c.sourceName
}

func (c *contextAdapter) Response() web.Response {
	return &c.response
}

func (c *contextAdapter) ResponseWriter() http.ResponseWriter {
	return c.echo.Response()
}

func (c *contextAdapter) SetResponseWriter(writer http.ResponseWriter) {
	c.echo.SetResponse(writer)
	c.refreshResponse()
}

func (c *contextAdapter) Bind(target any) error {
	return c.echo.Bind(target)
}

func (c *contextAdapter) Set(key string, value any) {
	c.echo.Set(key, value)
}

func (c *contextAdapter) Get(key string) any {
	return c.echo.Get(key)
}

func (c *contextAdapter) AddHeader(name string, value string) {
	c.echo.Response().Header().Add(name, value)
}

func (c *contextAdapter) SetHeader(name string, value string) {
	c.echo.Response().Header().Set(name, value)
}

func (c *contextAdapter) SetCookie(cookie *http.Cookie) {
	http.SetCookie(c.echo.Response(), cookie)
}

func (c *contextAdapter) JSON(code int, payload any) error {
	return c.echo.JSON(code, payload)
}

func (c *contextAdapter) Blob(code int, contentType string, body []byte) error {
	return c.echo.Blob(code, contentType, body)
}

func (c *contextAdapter) File(path string) error {
	http.ServeFile(c.echo.Response(), c.echo.Request(), path)
	return nil
}

func (c *contextAdapter) Text(code int, body string) error {
	return c.echo.String(code, body)
}

func (c *contextAdapter) HTML(code int, body string) error {
	return c.echo.HTML(code, body)
}

func (c *contextAdapter) NoContent(code int) error {
	return c.echo.NoContent(code)
}

func (c *contextAdapter) Redirect(code int, url string) error {
	return c.echo.Redirect(code, url)
}

func (c *contextAdapter) StatusCode() int {
	return c.response.StatusCode()
}

func (c *contextAdapter) Native() any {
	return c.echo
}

func (c *contextAdapter) DisableReuse() {
	c.reusable = false
}

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

func (c sourceNameContext) AppSourceName() string {
	return c.source
}

var _ web.Response = (*responseAdapter)(nil)

func (r *responseAdapter) Header() http.Header {
	return r.context.echo.Response().Header()
}

func (r *responseAdapter) Writer() http.ResponseWriter {
	return r.context.echo.Response()
}

func (r *responseAdapter) SetWriter(writer http.ResponseWriter) {
	r.context.echo.SetResponse(writer)
	r.context.refreshResponse()
}

func (r *responseAdapter) StatusCode() int {
	response := r.currentResponse()
	if response == nil {
		return 0
	}
	return response.Status
}

func (r *responseAdapter) Size() int64 {
	response := r.currentResponse()
	if response == nil {
		return 0
	}
	return response.Size
}

func (r *responseAdapter) Committed() bool {
	response := r.currentResponse()
	if response == nil {
		return false
	}
	return response.Committed
}

func (r *responseAdapter) Native() any {
	return r.context.echo.Response()
}

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
