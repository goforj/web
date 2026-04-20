package webmiddleware

import (
	"net/http"
	"strings"

	"github.com/goforj/web"
)

const wwwPrefix = "www."

// RedirectConfig configures redirect middleware.
type RedirectConfig struct {
	Code int
}

// DefaultRedirectConfig is the default redirect config.
var DefaultRedirectConfig = RedirectConfig{
	Code: http.StatusMovedPermanently,
}

type redirectLogic func(scheme string, host string, uri string) (bool, string)

// HTTPSRedirect redirects http requests to https.
// @group Middleware - Redirects
// Example:
// req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
// ctx := webtest.NewContext(req, nil, "/docs", nil)
// _ = webmiddleware.HTTPSRedirect()(func(c web.Context) error { return nil })(ctx)
// fmt.Println(ctx.StatusCode(), ctx.Response().Header().Get("Location"))
//	// 301 https://example.com/docs
func HTTPSRedirect() web.Middleware {
	return HTTPSRedirectWithConfig(DefaultRedirectConfig)
}

// HTTPSRedirectWithConfig returns HTTPS redirect middleware with config.
// @group Middleware - Redirects
// Example:
// req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
// ctx := webtest.NewContext(req, nil, "/docs", nil)
// _ = webmiddleware.HTTPSRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})(func(c web.Context) error { return nil })(ctx)
// fmt.Println(ctx.StatusCode())
//	// 307
func HTTPSRedirectWithConfig(config RedirectConfig) web.Middleware {
	return redirect(config, func(scheme string, host string, uri string) (bool, string) {
		if scheme != "https" {
			return true, "https://" + host + uri
		}
		return false, ""
	})
}

// HTTPSWWWRedirect redirects to https + www.
// @group Middleware - Redirects
// Example:
// req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
// ctx := webtest.NewContext(req, nil, "/docs", nil)
// _ = webmiddleware.HTTPSWWWRedirect()(func(c web.Context) error { return nil })(ctx)
// fmt.Println(ctx.Response().Header().Get("Location"))
//	// https://www.example.com/docs
func HTTPSWWWRedirect() web.Middleware {
	return HTTPSWWWRedirectWithConfig(DefaultRedirectConfig)
}

// HTTPSWWWRedirectWithConfig returns HTTPS+WWW redirect middleware with config.
// @group Middleware - Redirects
// Example:
// req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
// ctx := webtest.NewContext(req, nil, "/docs", nil)
// _ = webmiddleware.HTTPSWWWRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})(func(c web.Context) error { return nil })(ctx)
// fmt.Println(ctx.StatusCode())
//	// 307
func HTTPSWWWRedirectWithConfig(config RedirectConfig) web.Middleware {
	return redirect(config, func(scheme string, host string, uri string) (bool, string) {
		if scheme != "https" || !strings.HasPrefix(host, wwwPrefix) {
			host = strings.TrimPrefix(host, wwwPrefix)
			return true, "https://" + wwwPrefix + host + uri
		}
		return false, ""
	})
}

// HTTPSNonWWWRedirect redirects to https without www.
// @group Middleware - Redirects
// Example:
// req := httptest.NewRequest(http.MethodGet, "http://www.example.com/docs", nil)
// ctx := webtest.NewContext(req, nil, "/docs", nil)
// _ = webmiddleware.HTTPSNonWWWRedirect()(func(c web.Context) error { return nil })(ctx)
// fmt.Println(ctx.Response().Header().Get("Location"))
//	// https://example.com/docs
func HTTPSNonWWWRedirect() web.Middleware {
	return HTTPSNonWWWRedirectWithConfig(DefaultRedirectConfig)
}

// HTTPSNonWWWRedirectWithConfig returns HTTPS non-WWW redirect middleware with config.
// @group Middleware - Redirects
// Example:
// req := httptest.NewRequest(http.MethodGet, "http://www.example.com/docs", nil)
// ctx := webtest.NewContext(req, nil, "/docs", nil)
// _ = webmiddleware.HTTPSNonWWWRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})(func(c web.Context) error { return nil })(ctx)
// fmt.Println(ctx.StatusCode())
//	// 307
func HTTPSNonWWWRedirectWithConfig(config RedirectConfig) web.Middleware {
	return redirect(config, func(scheme string, host string, uri string) (bool, string) {
		if scheme != "https" || strings.HasPrefix(host, wwwPrefix) {
			return true, "https://" + strings.TrimPrefix(host, wwwPrefix) + uri
		}
		return false, ""
	})
}

// WWWRedirect redirects to the www host.
// @group Middleware - Redirects
// Example:
// req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
// ctx := webtest.NewContext(req, nil, "/docs", nil)
// _ = webmiddleware.WWWRedirect()(func(c web.Context) error { return nil })(ctx)
// fmt.Println(ctx.Response().Header().Get("Location"))
//	// http://www.example.com/docs
func WWWRedirect() web.Middleware {
	return WWWRedirectWithConfig(DefaultRedirectConfig)
}

// WWWRedirectWithConfig returns WWW redirect middleware with config.
// @group Middleware - Redirects
// Example:
// req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
// ctx := webtest.NewContext(req, nil, "/docs", nil)
// _ = webmiddleware.WWWRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})(func(c web.Context) error { return nil })(ctx)
// fmt.Println(ctx.StatusCode())
//	// 307
func WWWRedirectWithConfig(config RedirectConfig) web.Middleware {
	return redirect(config, func(scheme string, host string, uri string) (bool, string) {
		if !strings.HasPrefix(host, wwwPrefix) {
			return true, scheme + "://" + wwwPrefix + host + uri
		}
		return false, ""
	})
}

// NonWWWRedirect redirects to the non-www host.
// @group Middleware - Redirects
// Example:
// req := httptest.NewRequest(http.MethodGet, "http://www.example.com/docs", nil)
// ctx := webtest.NewContext(req, nil, "/docs", nil)
// _ = webmiddleware.NonWWWRedirect()(func(c web.Context) error { return nil })(ctx)
// fmt.Println(ctx.Response().Header().Get("Location"))
//	// http://example.com/docs
func NonWWWRedirect() web.Middleware {
	return NonWWWRedirectWithConfig(DefaultRedirectConfig)
}

// NonWWWRedirectWithConfig returns non-WWW redirect middleware with config.
// @group Middleware - Redirects
// Example:
// req := httptest.NewRequest(http.MethodGet, "http://www.example.com/docs", nil)
// ctx := webtest.NewContext(req, nil, "/docs", nil)
// _ = webmiddleware.NonWWWRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})(func(c web.Context) error { return nil })(ctx)
// fmt.Println(ctx.StatusCode())
//	// 307
func NonWWWRedirectWithConfig(config RedirectConfig) web.Middleware {
	return redirect(config, func(scheme string, host string, uri string) (bool, string) {
		if strings.HasPrefix(host, wwwPrefix) {
			return true, scheme + "://" + strings.TrimPrefix(host, wwwPrefix) + uri
		}
		return false, ""
	})
}

func redirect(config RedirectConfig, logic redirectLogic) web.Middleware {
	if config.Code == 0 {
		config.Code = DefaultRedirectConfig.Code
	}
	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			if ok, target := logic(r.Scheme(), r.Host(), r.URI()); ok {
				return r.Redirect(config.Code, target)
			}
			return next(r)
		}
	}
}
