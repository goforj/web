package middleware

import (
	"time"

	"github.com/goforj/web"
)

// RequestLoggerValues are the values captured by request logger middleware.
type RequestLoggerValues struct {
	Status  int
	URI     string
	Method  string
	Latency time.Duration
	Error   error
}

// RequestLoggerConfig configures request logger middleware.
type RequestLoggerConfig struct {
	LogValuesFunc func(web.Context, RequestLoggerValues) error
}

// RequestLoggerWithConfig returns request logger middleware with config.
func RequestLoggerWithConfig(config RequestLoggerConfig) web.Middleware {
	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			start := time.Now()
			err := next(r)
			values := RequestLoggerValues{
				Status:  r.StatusCode(),
				URI:     r.URI(),
				Method:  r.Method(),
				Latency: time.Since(start),
				Error:   err,
			}
			if config.LogValuesFunc != nil {
				if logErr := config.LogValuesFunc(r, values); logErr != nil {
					return logErr
				}
			}
			return err
		}
	}
}
