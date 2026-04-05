package echoweb

import (
	"github.com/goforj/web"
	echo "github.com/labstack/echo/v4"
)

// Adapter owns an Echo engine while exposing the app-facing web.Router contract.
type Adapter struct {
	engine *echo.Echo
	router web.Router
}

// New creates a new Echo-backed web adapter.
func New() *Adapter {
	engine := echo.New()
	router := &routerAdapter{engine: engine, group: engine}
	engine.Use(adaptRouterMiddlewares(router))
	return &Adapter{
		engine: engine,
		router: router,
	}
}

// Wrap exposes an existing Echo engine through the web.Router contract.
func Wrap(engine *echo.Echo) *Adapter {
	if engine == nil {
		engine = echo.New()
	}
	router := &routerAdapter{engine: engine, group: engine}
	engine.Use(adaptRouterMiddlewares(router))
	return &Adapter{
		engine: engine,
		router: router,
	}
}

// Echo returns the underlying Echo engine.
func (a *Adapter) Echo() *echo.Echo {
	if a == nil {
		return nil
	}
	return a.engine
}

// Router returns the app-facing router contract.
func (a *Adapter) Router() web.Router {
	if a == nil {
		return nil
	}
	return a.router
}
