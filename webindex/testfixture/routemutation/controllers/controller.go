// Package controllers provides route mutation fixtures whose runtime behavior must match indexed output.
package controllers

import (
	"net/http"

	"github.com/goforj/web"
)

// Controller provides the runtime route mutation used by index soundness tests.
type Controller struct{}

// Routes replaces a protected route in place so stale constructor tracking would describe a different runtime API.
func (c *Controller) Routes() []web.Route {
	protected := web.NewRoute(http.MethodGet, "/protected", c.Protected, c.RequireAuth)
	public := web.NewRoute(http.MethodGet, "/public", c.Public)
	routes := []web.Route{protected}
	routes[0] = public
	return routes
}

// Protected returns the response associated with the discarded protected route.
func (c *Controller) Protected(ctx web.Context) error {
	return ctx.NoContent(http.StatusNoContent)
}

// Public returns the response associated with the actual runtime route.
func (c *Controller) Public(ctx web.Context) error {
	return ctx.NoContent(http.StatusNoContent)
}

// RequireAuth supplies the middleware whose stale provenance must never reach OpenAPI.
func (c *Controller) RequireAuth(next web.Handler) web.Handler {
	return next
}
