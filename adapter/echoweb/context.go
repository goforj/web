package echoweb

import (
	"context"
	"net/http"

	"github.com/goforj/web"
	echo "github.com/labstack/echo/v4"
)

type contextAdapter struct {
	echo echo.Context
}

var _ web.Context = (*contextAdapter)(nil)

func newContextAdapter(c echo.Context) *contextAdapter {
	return &contextAdapter{echo: c}
}

func (c *contextAdapter) Context() context.Context {
	return c.echo.Request().Context()
}

func (c *contextAdapter) Method() string {
	return c.echo.Request().Method
}

func (c *contextAdapter) Path() string {
	return c.echo.Path()
}

func (c *contextAdapter) URI() string {
	return c.echo.Request().RequestURI
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
	return c.echo.Request()
}

func (c *contextAdapter) SetRequest(request *http.Request) {
	c.echo.SetRequest(request)
}

func (c *contextAdapter) ResponseWriter() http.ResponseWriter {
	return c.echo.Response().Writer
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
	return c.echo.File(path)
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
	return c.echo.Response().Status
}

func (c *contextAdapter) Native() any {
	return c.echo
}

// UnwrapContext returns the underlying Echo context when the web.Context came from this adapter.
func UnwrapContext(ctx web.Context) (echo.Context, bool) {
	adapted, ok := ctx.(*contextAdapter)
	if !ok || adapted == nil || adapted.echo == nil {
		return nil, false
	}
	return adapted.echo, true
}
