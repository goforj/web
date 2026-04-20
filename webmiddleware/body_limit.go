package webmiddleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/goforj/web"
)

// BodyLimitConfig configures body limit middleware.
type BodyLimitConfig struct {
	Limit string
}

// BodyLimit returns middleware that limits request body size.
// @group Middleware
// Example:
// _ = webmiddleware.BodyLimit("2KB")
func BodyLimit(limit string) web.Middleware {
	return BodyLimitWithConfig(BodyLimitConfig{Limit: limit})
}

// BodyLimitWithConfig returns body limit middleware with config.
// @group Middleware
// Example:
// _ = webmiddleware.BodyLimitWithConfig(webmiddleware.BodyLimitConfig{Limit: "2KB"})
func BodyLimitWithConfig(config BodyLimitConfig) web.Middleware {
	limit, err := parseBodyLimit(config.Limit)
	if err != nil {
		panic(err)
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			req := r.Request()
			if req == nil || req.Body == nil {
				return next(r)
			}

			if req.ContentLength > limit {
				return r.Text(http.StatusRequestEntityTooLarge, http.StatusText(http.StatusRequestEntityTooLarge))
			}

			req.Body = http.MaxBytesReader(r.ResponseWriter(), req.Body, limit)
			r.SetRequest(req)

			err := next(r)
			if err != nil {
				return err
			}
			return nil
		}
	}
}

func parseBodyLimit(raw string) (int64, error) {
	value := strings.TrimSpace(strings.ToUpper(raw))
	if value == "" {
		return 0, invalidConfigError("body-limit middleware requires a limit")
	}

	multiplier := int64(1)
	switch {
	case strings.HasSuffix(value, "KB"):
		multiplier = 1 << 10
		value = strings.TrimSpace(strings.TrimSuffix(value, "KB"))
	case strings.HasSuffix(value, "MB"):
		multiplier = 1 << 20
		value = strings.TrimSpace(strings.TrimSuffix(value, "MB"))
	case strings.HasSuffix(value, "GB"):
		multiplier = 1 << 30
		value = strings.TrimSpace(strings.TrimSuffix(value, "GB"))
	case strings.HasSuffix(value, "B"):
		value = strings.TrimSpace(strings.TrimSuffix(value, "B"))
	}

	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, invalidConfigError("invalid body limit: " + raw)
	}
	return n * multiplier, nil
}
