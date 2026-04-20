package webmiddleware

import (
	"net/http"

	"github.com/goforj/web"
)

// MethodOverrideGetter gets an override method from the request.
type MethodOverrideGetter func(web.Context) string

// MethodOverrideConfig configures method override middleware.
type MethodOverrideConfig struct {
	Getter MethodOverrideGetter
}

// DefaultMethodOverrideConfig is the default method override config.
var DefaultMethodOverrideConfig = MethodOverrideConfig{
	Getter: MethodFromHeader("X-HTTP-Method-Override"),
}

// MethodOverride returns method override middleware.
// @group Middleware
// Example:
// req := httptest.NewRequest(http.MethodPost, "/", nil)
// req.Header.Set("X-HTTP-Method-Override", http.MethodPatch)
// ctx := webtest.NewContext(req, nil, "/", nil)
// handler := webmiddleware.MethodOverride()(func(c web.Context) error {
// 	fmt.Println(c.Method())
// 	return nil
// })
// _ = handler(ctx)
//	// PATCH
func MethodOverride() web.Middleware {
	return MethodOverrideWithConfig(DefaultMethodOverrideConfig)
}

// MethodOverrideWithConfig returns method override middleware with config.
// @group Middleware
// Example:
// req := httptest.NewRequest(http.MethodPost, "/?_method=DELETE", nil)
// ctx := webtest.NewContext(req, nil, "/", nil)
// handler := webmiddleware.MethodOverrideWithConfig(webmiddleware.MethodOverrideConfig{
// 	Getter: webmiddleware.MethodFromQuery("_method"),
// })(func(c web.Context) error {
// 	fmt.Println(c.Method())
// 	return nil
// })
// _ = handler(ctx)
//	// DELETE
func MethodOverrideWithConfig(config MethodOverrideConfig) web.Middleware {
	if config.Getter == nil {
		config.Getter = DefaultMethodOverrideConfig.Getter
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			req := r.Request()
			if req != nil && req.Method == http.MethodPost {
				if method := config.Getter(r); method != "" {
					req.Method = method
					r.SetRequest(req)
				}
			}
			return next(r)
		}
	}
}

// MethodFromHeader gets an override method from a request header.
// @group Middleware
// Example:
// getter := webmiddleware.MethodFromHeader("X-HTTP-Method-Override")
// ctx := webtest.NewContext(nil, nil, "/", nil)
// ctx.Request().Header.Set("X-HTTP-Method-Override", "PATCH")
// fmt.Println(getter(ctx))
//	// PATCH
func MethodFromHeader(header string) MethodOverrideGetter {
	return func(r web.Context) string {
		return r.Header(header)
	}
}

// MethodFromForm gets an override method from a form field.
// @group Middleware
// Example:
// getter := webmiddleware.MethodFromForm("_method")
// req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("_method=DELETE"))
// req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
// ctx := webtest.NewContext(req, nil, "/", nil)
// fmt.Println(getter(ctx))
//	// DELETE
func MethodFromForm(param string) MethodOverrideGetter {
	return func(r web.Context) string {
		req := r.Request()
		if req == nil {
			return ""
		}
		if err := req.ParseForm(); err != nil {
			return ""
		}
		return req.FormValue(param)
	}
}

// MethodFromQuery gets an override method from a query parameter.
// @group Middleware
// Example:
// getter := webmiddleware.MethodFromQuery("_method")
// req := httptest.NewRequest(http.MethodPost, "/?_method=PUT", nil)
// ctx := webtest.NewContext(req, nil, "/", nil)
// fmt.Println(getter(ctx))
//	// PUT
func MethodFromQuery(param string) MethodOverrideGetter {
	return func(r web.Context) string {
		return r.Query(param)
	}
}
