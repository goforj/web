// Package controllers provides route providers used to compare runtime registration with source indexing.
package controllers

import (
	"net/http"

	"github.com/goforj/web"
)

// PublicController exposes routes that do not require authentication.
type PublicController struct{}

// PublicRoutes returns the public half of the fixture's generated-style composition.
func (*PublicController) PublicRoutes() []web.Route {
	return []web.Route{
		web.NewRoute(http.MethodGet, "/health", health),
		web.NewRoute(http.MethodPost, "/sessions", createSession),
	}
}

// AccountController exposes routes that share group and route middleware.
type AccountController struct{}

// ProtectedRoutes returns the authenticated half of the fixture's generated-style composition.
func (*AccountController) ProtectedRoutes() []web.Route {
	return []web.Route{
		web.NewRoute(http.MethodGet, "/accounts/:id", account, audit),
	}
}

// InternalRoutes is deliberately unreturned so the index must match the runtime surface rather than every source declaration.
func (*AccountController) InternalRoutes() []web.Route {
	return []web.Route{
		web.NewRoute(http.MethodDelete, "/internal/accounts/:id", deleteAccount),
	}
}

// health returns the public fixture response.
func health(ctx web.Context) error {
	return ctx.NoContent(http.StatusNoContent)
}

// createSession returns a small anonymous response to exercise native response analysis.
func createSession(ctx web.Context) error {
	return ctx.JSON(http.StatusCreated, map[string]any{"created": true})
}

// account reads the route parameter represented in both runtime and indexed paths.
func account(ctx web.Context) error {
	return ctx.JSON(http.StatusOK, map[string]any{"id": ctx.Param("id")})
}

// deleteAccount exists only on the deliberately unreturned route provider.
func deleteAccount(ctx web.Context) error {
	return ctx.NoContent(http.StatusNoContent)
}

// audit is a route-level fixture middleware whose presence is asserted independently from route parity.
func audit(next web.Handler) web.Handler {
	return next
}
