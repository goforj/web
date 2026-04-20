package webmiddleware

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
// @group Middleware
// Example:
// mw := webmiddleware.RequestID()
// handler := mw(func(c web.Context) error {
// 	fmt.Println(c.Get("request_id") != nil)
// 	return c.NoContent(http.StatusOK)
// })
// ctx := webtest.NewContext(nil, nil, "/", nil)
// _ = handler(ctx)
// fmt.Println(ctx.Response().Header().Get("X-Request-ID") != "")
//	// true
//	// true
func RequestID() web.Middleware {
	return RequestIDWithConfig(DefaultRequestIDConfig)
}

// RequestIDWithConfig returns RequestID middleware with config.
// @group Middleware
// Example:
// mw := webmiddleware.RequestIDWithConfig(webmiddleware.RequestIDConfig{
// 	Generator: func() string { return "fixed-id" },
// })
// handler := mw(func(c web.Context) error { return c.NoContent(http.StatusOK) })
// ctx := webtest.NewContext(nil, nil, "/", nil)
// _ = handler(ctx)
// fmt.Println(ctx.Response().Header().Get("X-Request-ID"))
//	// fixed-id
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
