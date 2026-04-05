package middleware

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/goforj/web"
)

// RequestIDConfig configures request id middleware.
type RequestIDConfig struct {
	Generator        func() string
	TargetHeader     string
	ContextKey       string
	RequestIDHandler func(web.Context, string)
}

// DefaultRequestIDConfig is the default RequestID middleware config.
var DefaultRequestIDConfig = RequestIDConfig{
	Generator:    defaultRequestIDGenerator,
	TargetHeader: "X-Request-ID",
	ContextKey:   "request_id",
}

// RequestID returns middleware that sets a request id header and context value.
func RequestID() web.Middleware {
	return RequestIDWithConfig(DefaultRequestIDConfig)
}

// RequestIDWithConfig returns RequestID middleware with config.
func RequestIDWithConfig(config RequestIDConfig) web.Middleware {
	if config.Generator == nil {
		config.Generator = DefaultRequestIDConfig.Generator
	}
	if config.TargetHeader == "" {
		config.TargetHeader = DefaultRequestIDConfig.TargetHeader
	}
	if config.ContextKey == "" {
		config.ContextKey = DefaultRequestIDConfig.ContextKey
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			requestID := r.Header(config.TargetHeader)
			if requestID == "" {
				requestID = config.Generator()
			}
			r.SetHeader(config.TargetHeader, requestID)
			r.Set(config.ContextKey, requestID)
			if config.RequestIDHandler != nil {
				config.RequestIDHandler(r, requestID)
			}
			return next(r)
		}
	}
}

func defaultRequestIDGenerator() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return "00000000000000000000000000000000"
}
