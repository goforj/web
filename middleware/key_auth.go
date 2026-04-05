package middleware

import (
	"errors"
	"net/http"
	"strings"

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
func KeyAuth(fn KeyAuthValidator) web.Middleware {
	config := DefaultKeyAuthConfig
	config.Validator = fn
	return KeyAuthWithConfig(config)
}

// KeyAuthWithConfig returns key auth middleware with config.
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

type valuesExtractor func(web.Context) ([]string, error)

var (
	errHeaderExtractorValueMissing = errors.New("missing key in request header")
	errHeaderExtractorValueInvalid = errors.New("invalid key in request header")
	errQueryExtractorValueMissing  = errors.New("missing key in the query string")
	errCookieExtractorValueMissing = errors.New("missing key in cookies")
	errFormExtractorValueMissing   = errors.New("missing key in the form")
)

func createExtractors(lookups string, authScheme string) ([]valuesExtractor, error) {
	parts := strings.Split(lookups, ",")
	out := make([]valuesExtractor, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		source, name, cutPrefix, err := parseLookup(part, authScheme)
		if err != nil {
			return nil, err
		}
		switch source {
		case "header":
			out = append(out, headerExtractor(name, cutPrefix))
		case "query":
			out = append(out, queryExtractor(name))
		case "cookie":
			out = append(out, cookieExtractor(name))
		case "form":
			out = append(out, formExtractor(name))
		default:
			return nil, invalidConfigError("unsupported key lookup source: " + source)
		}
	}
	if len(out) == 0 {
		return nil, invalidConfigError("key-auth middleware requires at least one key lookup")
	}
	return out, nil
}

func parseLookup(raw string, authScheme string) (source string, name string, cutPrefix string, err error) {
	parts := strings.Split(raw, ":")
	if len(parts) < 2 {
		return "", "", "", invalidConfigError("invalid key lookup: " + raw)
	}
	source = strings.TrimSpace(parts[0])
	name = strings.TrimSpace(parts[1])
	if source == "" || name == "" {
		return "", "", "", invalidConfigError("invalid key lookup: " + raw)
	}
	if len(parts) > 2 {
		cutPrefix = strings.TrimSpace(parts[2])
	} else if strings.EqualFold(name, "Authorization") && authScheme != "" {
		cutPrefix = authScheme
	}
	if cutPrefix != "" && !strings.HasSuffix(cutPrefix, " ") {
		cutPrefix += " "
	}
	return source, name, cutPrefix, nil
}

func headerExtractor(name string, cutPrefix string) valuesExtractor {
	return func(r web.Context) ([]string, error) {
		value := r.Header(name)
		if value == "" {
			return nil, errHeaderExtractorValueMissing
		}
		if cutPrefix != "" {
			if len(value) <= len(cutPrefix) || !strings.EqualFold(value[:len(cutPrefix)], cutPrefix) {
				return nil, errHeaderExtractorValueInvalid
			}
			value = strings.TrimSpace(value[len(cutPrefix):])
		}
		if value == "" {
			return nil, errHeaderExtractorValueMissing
		}
		return []string{value}, nil
	}
}

func queryExtractor(name string) valuesExtractor {
	return func(r web.Context) ([]string, error) {
		value := strings.TrimSpace(r.Query(name))
		if value == "" {
			return nil, errQueryExtractorValueMissing
		}
		return []string{value}, nil
	}
}

func cookieExtractor(name string) valuesExtractor {
	return func(r web.Context) ([]string, error) {
		cookie, err := r.Cookie(name)
		if err != nil || cookie == nil || strings.TrimSpace(cookie.Value) == "" {
			return nil, errCookieExtractorValueMissing
		}
		return []string{cookie.Value}, nil
	}
}

func formExtractor(name string) valuesExtractor {
	return func(r web.Context) ([]string, error) {
		req, ok := nativeRequest(r)
		if !ok {
			return nil, errFormExtractorValueMissing
		}
		if err := req.ParseForm(); err != nil {
			return nil, err
		}
		values := req.Form[name]
		if len(values) == 0 {
			return nil, errFormExtractorValueMissing
		}
		out := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				out = append(out, value)
			}
		}
		if len(out) == 0 {
			return nil, errFormExtractorValueMissing
		}
		return out, nil
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
