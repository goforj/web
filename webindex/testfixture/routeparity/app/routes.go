// Package app composes the exact route groups used by the runtime parity fixture.
package app

import (
	"net/http"
	"slices"

	"github.com/goforj/web"
	"github.com/goforj/web/webindex/testfixture/routeparity/controllers"
)

// ProvideRoutes composes public and protected providers in the same shape generated GoForj apps use.
func ProvideRoutes(publicController *controllers.PublicController, accountController *controllers.AccountController) []web.RouteGroup {
	publicRoutes := slices.Concat(publicController.PublicRoutes())
	protectedRoutes := slices.Concat(accountController.ProtectedRoutes())
	unusedRoutes := slices.Concat(accountController.InternalRoutes())
	_ = unusedRoutes

	return []web.RouteGroup{
		web.NewRouteGroup("/api/v1", publicRoutes),
		web.NewRouteGroup("/api/v1", protectedRoutes, requireAuth),
		web.NewRouteGroup("/api/", []web.Route{web.NewRoute(http.MethodGet, "/double", exactPath)}),
		web.NewRouteGroup("/api", []web.Route{web.NewRoute(http.MethodGet, "relative", exactPath)}),
	}
}

// exactPath keeps unusual prefix/path combinations observable in the runtime parity surface.
func exactPath(ctx web.Context) error {
	return ctx.NoContent(http.StatusNoContent)
}

// requireAuth is the group-level fixture middleware shared by runtime registration and source indexing.
func requireAuth(next web.Handler) web.Handler {
	return next
}
