package webmiddleware

import (
	"errors"
	"net/http"

	"github.com/goforj/web"
)

// KeyAuthValidator validates an extracted auth key.
type KeyAuthValidator func(auth string, r web.Context) (bool, error)

// KeyAuthErrorHandler handles missing/invalid auth keys.
type KeyAuthErrorHandler func(err error, r web.Context) error

// KeyAuthConfig configures key auth middleware.
type KeyAuthConfig struct {
	KeyLookup              string
	AuthScheme             string
	Validator              KeyAuthValidator
	ErrorHandler           KeyAuthErrorHandler
	ContinueOnIgnoredError bool
}

// ErrKeyAuthMissing is returned when no key can be extracted.
type ErrKeyAuthMissing struct {
	Err error
}

// DefaultKeyAuthConfig is the default key auth middleware config.
var DefaultKeyAuthConfig = KeyAuthConfig{
	KeyLookup:  "header:Authorization",
	AuthScheme: "Bearer",
}

func (e *ErrKeyAuthMissing) Error() string {
	return e.Err.Error()
}

func (e *ErrKeyAuthMissing) Unwrap() error {
	return e.Err
}

// KeyAuth returns key auth middleware.
// @group Middleware
// Example:
// mw := webmiddleware.KeyAuth(func(key string, c web.Context) (bool, error) {
// 	return key == "demo-key", nil
// })
// _ = mw
//	// true
func KeyAuth(fn KeyAuthValidator) web.Middleware {
	config := DefaultKeyAuthConfig
	config.Validator = fn
	return KeyAuthWithConfig(config)
}

// KeyAuthWithConfig returns key auth middleware with config.
// @group Middleware
// Example:
// mw := webmiddleware.KeyAuthWithConfig(webmiddleware.KeyAuthConfig{
// 	Validator: func(key string, c web.Context) (bool, error) { return true, nil },
// })
// _ = mw
//	// true
func KeyAuthWithConfig(config KeyAuthConfig) web.Middleware {
	if config.AuthScheme == "" {
		config.AuthScheme = DefaultKeyAuthConfig.AuthScheme
	}
	if config.KeyLookup == "" {
		config.KeyLookup = DefaultKeyAuthConfig.KeyLookup
	}
	if config.Validator == nil {
		panic("web: key-auth middleware requires a validator function")
	}

	extractors, err := createExtractors(config.KeyLookup, config.AuthScheme)
	if err != nil {
		panic(err)
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			var lastExtractorErr error
			var lastValidatorErr error

			for _, extractor := range extractors {
				keys, err := extractor(r)
				if err != nil {
					lastExtractorErr = err
					continue
				}
				for _, key := range keys {
					valid, err := config.Validator(key, r)
					if err != nil {
						lastValidatorErr = err
						continue
					}
					if valid {
						return next(r)
					}
					lastValidatorErr = errors.New("invalid key")
				}
			}

			err := lastValidatorErr
			if err == nil {
				err = &ErrKeyAuthMissing{Err: normalizeExtractorError(lastExtractorErr)}
			}

			if config.ErrorHandler != nil {
				handledErr := config.ErrorHandler(err, r)
				if config.ContinueOnIgnoredError && handledErr == nil {
					return next(r)
				}
				return handledErr
			}

			if lastValidatorErr != nil {
				return r.Text(http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized))
			}
			return r.Text(http.StatusBadRequest, err.Error())
		}
	}
}

func normalizeExtractorError(err error) error {
	switch err {
	case nil:
		return errors.New("missing key")
	case errQueryExtractorValueMissing:
		return errors.New("missing key in the query string")
	case errCookieExtractorValueMissing:
		return errors.New("missing key in cookies")
	case errFormExtractorValueMissing:
		return errors.New("missing key in the form")
	case errHeaderExtractorValueMissing:
		return errors.New("missing key in request header")
	case errHeaderExtractorValueInvalid:
		return errors.New("invalid key in the request header")
	default:
		return err
	}
}
