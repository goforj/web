package webmiddleware

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/goforj/web"
)

// BasicAuthValidator validates a username/password pair.
type BasicAuthValidator func(string, string, web.Context) (bool, error)

// BasicAuthConfig configures basic auth middleware.
type BasicAuthConfig struct {
	Validator BasicAuthValidator
	Realm     string
}

const (
	basicAuthScheme  = "basic"
	defaultAuthRealm = "Restricted"
)

// DefaultBasicAuthConfig is the default basic auth middleware config.
var DefaultBasicAuthConfig = BasicAuthConfig{
	Realm: defaultAuthRealm,
}

// BasicAuth returns basic auth middleware.
// @group Middleware
// Example:
// mw := webmiddleware.BasicAuth(func(user, pass string, c web.Context) (bool, error) {
// 	return user == "demo" && pass == "secret", nil
// })
// _ = mw
func BasicAuth(fn BasicAuthValidator) web.Middleware {
	config := DefaultBasicAuthConfig
	config.Validator = fn
	return BasicAuthWithConfig(config)
}

// BasicAuthWithConfig returns basic auth middleware with config.
// @group Middleware
// Example:
// mw := webmiddleware.BasicAuthWithConfig(webmiddleware.BasicAuthConfig{
// 	Realm: "Example",
// 	Validator: func(user, pass string, c web.Context) (bool, error) { return true, nil },
// })
// _ = mw
func BasicAuthWithConfig(config BasicAuthConfig) web.Middleware {
	if config.Validator == nil {
		panic("web: basic-auth middleware requires a validator function")
	}
	if config.Realm == "" {
		config.Realm = defaultAuthRealm
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			auth := r.Header("Authorization")
			schemeLen := len(basicAuthScheme)

			if len(auth) > schemeLen+1 && strings.EqualFold(auth[:schemeLen], basicAuthScheme) {
				decoded, err := base64.StdEncoding.DecodeString(auth[schemeLen+1:])
				if err != nil {
					r.SetHeader("WWW-Authenticate", basicAuthChallenge(config.Realm))
					return r.Text(http.StatusBadRequest, http.StatusText(http.StatusBadRequest))
				}

				credentials := string(decoded)
				for i := 0; i < len(credentials); i++ {
					if credentials[i] != ':' {
						continue
					}
					valid, err := config.Validator(credentials[:i], credentials[i+1:], r)
					if err != nil {
						return err
					}
					if valid {
						return next(r)
					}
					break
				}
			}

			r.SetHeader("WWW-Authenticate", basicAuthChallenge(config.Realm))
			return r.Text(http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized))
		}
	}
}

func basicAuthChallenge(realm string) string {
	if realm == defaultAuthRealm {
		return basicAuthScheme + " realm=" + defaultAuthRealm
	}
	return fmt.Sprintf("%s realm=%s", basicAuthScheme, strconv.Quote(realm))
}
