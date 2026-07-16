package webmiddleware

import (
	"strings"

	"github.com/goforj/web"
)

// TrailingSlashConfig configures slash middleware.
type TrailingSlashConfig struct {
	RedirectCode int
}

// DefaultTrailingSlashConfig is the default trailing slash config.
var DefaultTrailingSlashConfig = TrailingSlashConfig{}

// AddTrailingSlash adds a trailing slash to the request path.
// @group Middleware - Path Rewriting
// Example:
// router := echoweb.New().Router()
// router.Pre(webmiddleware.AddTrailingSlash())
//
//	router.GET("/docs/", func(c web.Context) error {
//		return c.Text(200, "docs")
//	})
func AddTrailingSlash() web.Middleware {
	return AddTrailingSlashWithConfig(DefaultTrailingSlashConfig)
}

// AddTrailingSlashWithConfig returns trailing-slash middleware with config.
// @group Middleware - Path Rewriting
// Example:
// router := echoweb.New().Router()
//
//	router.Pre(webmiddleware.AddTrailingSlashWithConfig(webmiddleware.TrailingSlashConfig{
//		RedirectCode: 308,
//	}))
//
//	router.GET("/docs/", func(c web.Context) error {
//		return c.Text(200, "docs")
//	})
func AddTrailingSlashWithConfig(config TrailingSlashConfig) web.Middleware {
	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			req := r.Request()
			if req == nil || req.URL == nil {
				return next(r)
			}
			path := req.URL.Path
			if !strings.HasSuffix(path, "/") {
				path += "/"
				uri := path
				if query := req.URL.RawQuery; query != "" {
					uri += "?" + query
				}
				if config.RedirectCode != 0 {
					return r.Redirect(config.RedirectCode, sanitizeURI(uri))
				}
				req.URL.Path = path
				req.RequestURI = uri
				r.SetRequest(req)
			}
			return next(r)
		}
	}
}

// RemoveTrailingSlash removes the trailing slash from the request path.
// @group Middleware - Path Rewriting
// Example:
// router := echoweb.New().Router()
// router.Pre(webmiddleware.RemoveTrailingSlash())
//
//	router.GET("/docs", func(c web.Context) error {
//		return c.Text(200, "docs")
//	})
func RemoveTrailingSlash() web.Middleware {
	return RemoveTrailingSlashWithConfig(DefaultTrailingSlashConfig)
}

// RemoveTrailingSlashWithConfig returns remove-trailing-slash middleware with config.
// @group Middleware - Path Rewriting
// Example:
// router := echoweb.New().Router()
//
//	router.Pre(webmiddleware.RemoveTrailingSlashWithConfig(webmiddleware.TrailingSlashConfig{
//		RedirectCode: 308,
//	}))
//
//	router.GET("/docs", func(c web.Context) error {
//		return c.Text(200, "docs")
//	})
func RemoveTrailingSlashWithConfig(config TrailingSlashConfig) web.Middleware {
	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			req := r.Request()
			if req == nil || req.URL == nil {
				return next(r)
			}
			path := req.URL.Path
			if len(path) > 1 && strings.HasSuffix(path, "/") {
				path = strings.TrimSuffix(path, "/")
				uri := path
				if query := req.URL.RawQuery; query != "" {
					uri += "?" + query
				}
				if config.RedirectCode != 0 {
					return r.Redirect(config.RedirectCode, sanitizeURI(uri))
				}
				req.URL.Path = path
				req.RequestURI = uri
				r.SetRequest(req)
			}
			return next(r)
		}
	}
}

// sanitizeURI normalizes an incoming URI so slash middleware can rewrite it safely.
func sanitizeURI(uri string) string {
	if len(uri) > 1 && (uri[0] == '\\' || uri[0] == '/') && (uri[1] == '\\' || uri[1] == '/') {
		uri = "/" + strings.TrimLeft(uri, `/\`)
	}
	return uri
}
