package webmiddleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/goforj/web"
)

// CSRFErrorHandler handles CSRF failures.
type CSRFErrorHandler func(error, web.Context) error

// CSRFConfig configures CSRF protection.
type CSRFConfig struct {
	Skipper        Skipper
	TokenLength    uint8
	TokenLookup    string
	ContextKey     string
	CookieName     string
	CookieDomain   string
	CookiePath     string
	CookieMaxAge   int
	CookieSecure   bool
	CookieHTTPOnly bool
	CookieSameSite http.SameSite
	ErrorHandler   CSRFErrorHandler

	generator func(uint8) string
}

// DefaultCSRFConfig is the default CSRF config.
var DefaultCSRFConfig = CSRFConfig{
	Skipper:        DefaultSkipper,
	TokenLength:    32,
	TokenLookup:    "header:X-CSRF-Token",
	ContextKey:     "csrf",
	CookieName:     "_csrf",
	CookieMaxAge:   86400,
	CookieSameSite: http.SameSiteDefaultMode,
}

// CSRF enables token-based CSRF protection.
func CSRF() web.Middleware {
	return CSRFWithConfig(DefaultCSRFConfig)
}

// CSRFWithConfig enables token-based CSRF protection with config.
func CSRFWithConfig(config CSRFConfig) web.Middleware {
	if config.Skipper == nil {
		config.Skipper = DefaultCSRFConfig.Skipper
	}
	if config.TokenLength == 0 {
		config.TokenLength = DefaultCSRFConfig.TokenLength
	}
	if config.TokenLookup == "" {
		config.TokenLookup = DefaultCSRFConfig.TokenLookup
	}
	if config.ContextKey == "" {
		config.ContextKey = DefaultCSRFConfig.ContextKey
	}
	if config.CookieName == "" {
		config.CookieName = DefaultCSRFConfig.CookieName
	}
	if config.CookieMaxAge == 0 {
		config.CookieMaxAge = DefaultCSRFConfig.CookieMaxAge
	}
	if config.CookieSameSite == http.SameSiteNoneMode {
		config.CookieSecure = true
	}
	if config.ErrorHandler == nil {
		config.ErrorHandler = func(err error, r web.Context) error {
			return r.JSON(http.StatusForbidden, map[string]any{
				"error": "invalid csrf token",
			})
		}
	}

	tokenGenerator := config.generator
	if tokenGenerator == nil {
		tokenGenerator = randomString
	}

	extractors, err := createExtractors(config.TokenLookup, "")
	if err != nil {
		panic(err)
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			if config.Skipper(r) {
				return next(r)
			}

			token := ""
			if cookie, err := r.Cookie(config.CookieName); err == nil && cookie != nil {
				token = cookie.Value
			}
			if token == "" {
				token = tokenGenerator(config.TokenLength)
			}

			switch r.Method() {
			case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			default:
				valid := false
				for _, extractor := range extractors {
					clientTokens, err := extractor(r)
					if err != nil {
						continue
					}
					for _, clientToken := range clientTokens {
						if validateCSRFToken(token, clientToken) {
							valid = true
							break
						}
					}
					if valid {
						break
					}
				}
				if !valid {
					return config.ErrorHandler(nil, r)
				}
			}

			cookie := &http.Cookie{
				Name:     config.CookieName,
				Value:    token,
				Expires:  time.Now().Add(time.Duration(config.CookieMaxAge) * time.Second),
				Secure:   config.CookieSecure,
				HttpOnly: config.CookieHTTPOnly,
				SameSite: config.CookieSameSite,
			}
			if config.CookiePath != "" {
				cookie.Path = config.CookiePath
			}
			if config.CookieDomain != "" {
				cookie.Domain = config.CookieDomain
			}
			r.SetCookie(cookie)
			r.Set(config.ContextKey, token)
			r.AddHeader("Vary", "Cookie")

			return next(r)
		}
	}
}

func validateCSRFToken(token, clientToken string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(clientToken)) == 1
}

func randomString(length uint8) string {
	if length == 0 {
		return ""
	}
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	if len(token) > int(length) {
		return token[:length]
	}
	return token
}
