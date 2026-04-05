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
func MethodOverride() web.Middleware {
	return MethodOverrideWithConfig(DefaultMethodOverrideConfig)
}

// MethodOverrideWithConfig returns method override middleware with config.
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
func MethodFromHeader(header string) MethodOverrideGetter {
	return func(r web.Context) string {
		return r.Header(header)
	}
}

// MethodFromForm gets an override method from a form field.
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
func MethodFromQuery(param string) MethodOverrideGetter {
	return func(r web.Context) string {
		return r.Query(param)
	}
}
