package middleware

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
func HTTPSRedirect() web.Middleware {
	return HTTPSRedirectWithConfig(DefaultRedirectConfig)
}

func HTTPSRedirectWithConfig(config RedirectConfig) web.Middleware {
	return redirect(config, func(scheme string, host string, uri string) (bool, string) {
		if scheme != "https" {
			return true, "https://" + host + uri
		}
		return false, ""
	})
}

// HTTPSWWWRedirect redirects to https + www.
func HTTPSWWWRedirect() web.Middleware {
	return HTTPSWWWRedirectWithConfig(DefaultRedirectConfig)
}

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
func HTTPSNonWWWRedirect() web.Middleware {
	return HTTPSNonWWWRedirectWithConfig(DefaultRedirectConfig)
}

func HTTPSNonWWWRedirectWithConfig(config RedirectConfig) web.Middleware {
	return redirect(config, func(scheme string, host string, uri string) (bool, string) {
		if scheme != "https" || strings.HasPrefix(host, wwwPrefix) {
			return true, "https://" + strings.TrimPrefix(host, wwwPrefix) + uri
		}
		return false, ""
	})
}

// WWWRedirect redirects to the www host.
func WWWRedirect() web.Middleware {
	return WWWRedirectWithConfig(DefaultRedirectConfig)
}

func WWWRedirectWithConfig(config RedirectConfig) web.Middleware {
	return redirect(config, func(scheme string, host string, uri string) (bool, string) {
		if !strings.HasPrefix(host, wwwPrefix) {
			return true, scheme + "://" + wwwPrefix + host + uri
		}
		return false, ""
	})
}

// NonWWWRedirect redirects to the non-www host.
func NonWWWRedirect() web.Middleware {
	return NonWWWRedirectWithConfig(DefaultRedirectConfig)
}

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
