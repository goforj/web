package webmiddleware

import (
	"fmt"
	"strings"

	"github.com/goforj/web"
)

// SecureConfig configures secure response headers.
type SecureConfig struct {
	Skipper                Skipper
	XSSProtection          string
	ContentTypeNosniff     string
	XFrameOptions          string
	HSTSMaxAge             int
	HSTSExcludeSubdomains  bool
	ContentSecurityPolicy  string
	CSPReportOnly          bool
	HSTSPreloadEnabled     bool
	ReferrerPolicy         string
}

// DefaultSecureConfig is the default secure middleware config.
var DefaultSecureConfig = SecureConfig{
	Skipper:            DefaultSkipper,
	XSSProtection:      "1; mode=block",
	ContentTypeNosniff: "nosniff",
	XFrameOptions:      "SAMEORIGIN",
}

// Secure sets security-oriented response headers.
// @group Middleware
// Example:
// ctx := webtest.NewContext(nil, nil, "/", nil)
// handler := webmiddleware.Secure()(func(c web.Context) error { return c.NoContent(http.StatusOK) })
// _ = handler(ctx)
// fmt.Println(ctx.Response().Header().Get("X-Frame-Options"))
//	// SAMEORIGIN
func Secure() web.Middleware {
	return SecureWithConfig(DefaultSecureConfig)
}

// SecureWithConfig sets security-oriented response headers with config.
// @group Middleware
// Example:
// ctx := webtest.NewContext(nil, nil, "/", nil)
// handler := webmiddleware.SecureWithConfig(webmiddleware.SecureConfig{ReferrerPolicy: "same-origin"})(func(c web.Context) error {
// 	return c.NoContent(http.StatusOK)
// })
// _ = handler(ctx)
// fmt.Println(ctx.Response().Header().Get("Referrer-Policy"))
//	// same-origin
func SecureWithConfig(config SecureConfig) web.Middleware {
	if config.Skipper == nil {
		config.Skipper = DefaultSecureConfig.Skipper
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			if config.Skipper(r) {
				return next(r)
			}

			req := r.Request()
			if config.XSSProtection != "" {
				r.SetHeader("X-XSS-Protection", config.XSSProtection)
			}
			if config.ContentTypeNosniff != "" {
				r.SetHeader("X-Content-Type-Options", config.ContentTypeNosniff)
			}
			if config.XFrameOptions != "" {
				r.SetHeader("X-Frame-Options", config.XFrameOptions)
			}
			if req != nil && requestIsHTTPS(r) && config.HSTSMaxAge != 0 {
				subdomains := ""
				if !config.HSTSExcludeSubdomains {
					subdomains = "; includeSubDomains"
				}
				if config.HSTSPreloadEnabled {
					subdomains += "; preload"
				}
				r.SetHeader("Strict-Transport-Security", fmt.Sprintf("max-age=%d%s", config.HSTSMaxAge, subdomains))
			}
			if config.ContentSecurityPolicy != "" {
				header := "Content-Security-Policy"
				if config.CSPReportOnly {
					header = "Content-Security-Policy-Report-Only"
				}
				r.SetHeader(header, config.ContentSecurityPolicy)
			}
			if config.ReferrerPolicy != "" {
				r.SetHeader("Referrer-Policy", config.ReferrerPolicy)
			}
			return next(r)
		}
	}
}

func requestIsHTTPS(r web.Context) bool {
	if strings.EqualFold(r.Scheme(), "https") {
		return true
	}
	req := r.Request()
	if req == nil {
		return false
	}
	return strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https")
}
