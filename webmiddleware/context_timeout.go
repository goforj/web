package webmiddleware

import (
	"context"
	"errors"
	"time"

	"github.com/goforj/web"
)

// ContextTimeoutConfig configures request context timeouts.
type ContextTimeoutConfig struct {
	Skipper      Skipper
	ErrorHandler func(error, web.Context) error
	Timeout      time.Duration
}

// ContextTimeout sets a timeout on the request context.
// @group Middleware
// Example:
// _ = webmiddleware.ContextTimeout(2 * time.Second)
func ContextTimeout(timeout time.Duration) web.Middleware {
	return ContextTimeoutWithConfig(ContextTimeoutConfig{Timeout: timeout})
}

// ContextTimeoutWithConfig sets a timeout on the request context with config.
// @group Middleware
// Example:
// _ = webmiddleware.ContextTimeoutWithConfig(webmiddleware.ContextTimeoutConfig{Timeout: time.Second})
func ContextTimeoutWithConfig(config ContextTimeoutConfig) web.Middleware {
	if config.Timeout == 0 {
		panic("web: context timeout requires a timeout")
	}
	if config.Skipper == nil {
		config.Skipper = DefaultSkipper
	}
	if config.ErrorHandler == nil {
		config.ErrorHandler = func(err error, r web.Context) error {
			if err != nil && errors.Is(err, context.DeadlineExceeded) {
				return r.JSON(503, map[string]any{
					"error": "service unavailable",
				})
			}
			return err
		}
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			if config.Skipper(r) {
				return next(r)
			}

			req := r.Request()
			if req == nil {
				return next(r)
			}
			timeoutCtx, cancel := context.WithTimeout(req.Context(), config.Timeout)
			defer cancel()

			r.SetRequest(req.WithContext(timeoutCtx))
			if err := next(r); err != nil {
				return config.ErrorHandler(err, r)
			}
			return nil
		}
	}
}
