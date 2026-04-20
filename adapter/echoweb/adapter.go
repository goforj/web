package echoweb

import (
	"net/http"

	"github.com/goforj/web"
	echo "github.com/labstack/echo/v5"
)

// Adapter owns an Echo engine while exposing the app-facing web.Router contract.
type Adapter struct {
	engine *echo.Echo
	router web.Router
}

// New creates a new Echo-backed web adapter.
// @group Adapter
// Example:
// adapter := echoweb.New()
// _ = adapter.Router()
func New() *Adapter {
	engine := echo.New()
	engine.IPExtractor = echo.LegacyIPExtractor()
	router := &routerAdapter{engine: engine, group: engine}
	engine.Use(adaptRouterMiddlewares(router))
	return &Adapter{
		engine: engine,
		router: router,
	}
}

// Wrap exposes an existing Echo engine through the web.Router contract.
// @group Adapter
// Example:
// adapter := echoweb.Wrap(nil)
// _ = adapter.Echo()
func Wrap(engine *echo.Echo) *Adapter {
	if engine == nil {
		engine = echo.New()
	}
	if engine.IPExtractor == nil {
		engine.IPExtractor = echo.LegacyIPExtractor()
	}
	router := &routerAdapter{engine: engine, group: engine}
	engine.Use(adaptRouterMiddlewares(router))
	return &Adapter{
		engine: engine,
		router: router,
	}
}

// Echo returns the underlying Echo engine.
// @group Adapter
// Example:
// adapter := echoweb.New()
// _ = adapter.Echo()
func (a *Adapter) Echo() *echo.Echo {
	if a == nil {
		return nil
	}
	return a.engine
}

// Router returns the app-facing router contract.
// @group Adapter
// Example:
// adapter := echoweb.New()
// _ = adapter.Router()
func (a *Adapter) Router() web.Router {
	if a == nil {
		return nil
	}
	return a.router
}

// ServeHTTP exposes the adapter as a standard http.Handler.
// @group Adapter
// Example:
// adapter := echoweb.New()
// adapter.Router().GET("/healthz", func(c web.Context) error { return c.NoContent(http.StatusOK) })
// rr := httptest.NewRecorder()
// req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
// adapter.ServeHTTP(rr, req)
// fmt.Println(rr.Code)
//	// 204
func (a *Adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a == nil || a.engine == nil {
		http.NotFound(w, r)
		return
	}
	a.engine.ServeHTTP(w, r)
}
