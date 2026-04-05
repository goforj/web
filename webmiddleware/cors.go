package webmiddleware

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/goforj/web"
)

// CORSConfig configures CORS middleware.
type CORSConfig struct {
	AllowOrigins     []string
	AllowOriginFunc  func(origin string) (bool, error)
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
	ExposeHeaders    []string
	MaxAge           int
}

// DefaultCORSConfig is the default CORS middleware config.
var DefaultCORSConfig = CORSConfig{
	AllowOrigins: []string{"*"},
	AllowMethods: []string{
		http.MethodGet,
		http.MethodHead,
		http.MethodPut,
		http.MethodPatch,
		http.MethodPost,
		http.MethodDelete,
	},
}

// CORS returns Cross-Origin Resource Sharing middleware.
func CORS() web.Middleware {
	return CORSWithConfig(DefaultCORSConfig)
}

// CORSWithConfig returns CORS middleware with config.
func CORSWithConfig(config CORSConfig) web.Middleware {
	if len(config.AllowOrigins) == 0 {
		config.AllowOrigins = DefaultCORSConfig.AllowOrigins
	}
	if len(config.AllowMethods) == 0 {
		config.AllowMethods = DefaultCORSConfig.AllowMethods
	}

	allowOriginPatterns := make([]*regexp.Regexp, 0, len(config.AllowOrigins))
	for _, origin := range config.AllowOrigins {
		if origin == "*" {
			continue
		}
		pattern := regexp.QuoteMeta(origin)
		pattern = strings.ReplaceAll(pattern, "\\*", ".*")
		pattern = strings.ReplaceAll(pattern, "\\?", ".")
		pattern = "^" + pattern + "$"
		re, err := regexp.Compile(pattern)
		if err == nil {
			allowOriginPatterns = append(allowOriginPatterns, re)
		}
	}

	allowMethods := strings.Join(config.AllowMethods, ",")
	allowHeaders := strings.Join(config.AllowHeaders, ",")
	exposeHeaders := strings.Join(config.ExposeHeaders, ",")

	maxAge := "0"
	if config.MaxAge > 0 {
		maxAge = strconv.Itoa(config.MaxAge)
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			origin := r.Header("Origin")
			preflight := r.Method() == http.MethodOptions

			r.AddHeader("Vary", "Origin")

			if origin == "" {
				if preflight {
					return r.NoContent(http.StatusNoContent)
				}
				return next(r)
			}

			allowOrigin, err := corsAllowedOrigin(origin, config, allowOriginPatterns)
			if err != nil {
				return err
			}
			if allowOrigin == "" {
				if preflight {
					return r.NoContent(http.StatusNoContent)
				}
				return next(r)
			}

			r.SetHeader("Access-Control-Allow-Origin", allowOrigin)
			if config.AllowCredentials {
				r.SetHeader("Access-Control-Allow-Credentials", "true")
			}

			if !preflight {
				if exposeHeaders != "" {
					r.SetHeader("Access-Control-Expose-Headers", exposeHeaders)
				}
				return next(r)
			}

			r.AddHeader("Vary", "Access-Control-Request-Method")
			r.AddHeader("Vary", "Access-Control-Request-Headers")
			r.SetHeader("Access-Control-Allow-Methods", allowMethods)
			if allowHeaders != "" {
				r.SetHeader("Access-Control-Allow-Headers", allowHeaders)
			} else if requested := r.Header("Access-Control-Request-Headers"); requested != "" {
				r.SetHeader("Access-Control-Allow-Headers", requested)
			}
			if config.MaxAge != 0 {
				r.SetHeader("Access-Control-Max-Age", maxAge)
			}
			return r.NoContent(http.StatusNoContent)
		}
	}
}

func corsAllowedOrigin(origin string, config CORSConfig, allowOriginPatterns []*regexp.Regexp) (string, error) {
	if config.AllowOriginFunc != nil {
		allowed, err := config.AllowOriginFunc(origin)
		if err != nil {
			return "", err
		}
		if allowed {
			return origin, nil
		}
		return "", nil
	}

	for _, item := range config.AllowOrigins {
		if item == "*" || item == origin {
			return item, nil
		}
		if corsMatchSubdomain(origin, item) {
			return origin, nil
		}
	}

	if len(origin) <= 261 && strings.Contains(origin, "://") {
		for _, re := range allowOriginPatterns {
			if re.MatchString(origin) {
				return origin, nil
			}
		}
	}
	return "", nil
}

func corsMatchSubdomain(origin string, pattern string) bool {
	if !strings.Contains(pattern, "*") {
		return false
	}
	replacer := regexp.QuoteMeta(pattern)
	replacer = strings.ReplaceAll(replacer, "\\*", ".*")
	re, err := regexp.Compile("^" + replacer + "$")
	if err != nil {
		return false
	}
	return re.MatchString(origin)
}
