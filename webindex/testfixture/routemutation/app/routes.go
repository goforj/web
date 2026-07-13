// Package app defines the route-composition boundary used by runtime-mutation parity tests.
package app

import (
	"github.com/goforj/web"
	"github.com/goforj/web/webindex/testfixture/routemutation/controllers"
)

// ProvideRoutes returns the actual runtime group for the route-mutation fixture.
func ProvideRoutes(controller *controllers.Controller) []web.RouteGroup {
	return []web.RouteGroup{web.NewRouteGroup("/api", controller.Routes())}
}
