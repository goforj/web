package webmiddleware

import (
	"bytes"
	"io"
	"net/http"

	"github.com/goforj/web"
)

// ErrorBodyDumpHandler receives non-success response bodies.
type ErrorBodyDumpHandler func(web.Context, int, []byte)

// ErrorBodyDumpConfig configures non-success response body capture.
type ErrorBodyDumpConfig struct {
	Skipper Skipper
	Handler ErrorBodyDumpHandler
}

// DefaultErrorBodyDumpConfig is the default config.
var DefaultErrorBodyDumpConfig = ErrorBodyDumpConfig{
	Skipper: DefaultSkipper,
}

// ErrorBodyDump captures response bodies for non-2xx and non-3xx responses.
// @group Middleware
// Example:
// mw := webmiddleware.ErrorBodyDump(func(c web.Context, status int, body []byte) {})
// _ = mw
//	// true
func ErrorBodyDump(handler ErrorBodyDumpHandler) web.Middleware {
	config := DefaultErrorBodyDumpConfig
	config.Handler = handler
	return ErrorBodyDumpWithConfig(config)
}

// ErrorBodyDumpWithConfig captures response bodies for non-success responses with config.
// @group Middleware
// Example:
// mw := webmiddleware.ErrorBodyDumpWithConfig(webmiddleware.ErrorBodyDumpConfig{
// 	Handler: func(c web.Context, status int, body []byte) {},
// })
// _ = mw
//	// true
func ErrorBodyDumpWithConfig(config ErrorBodyDumpConfig) web.Middleware {
	if config.Handler == nil {
		panic("web: error body dump middleware requires a handler")
	}
	if config.Skipper == nil {
		config.Skipper = DefaultErrorBodyDumpConfig.Skipper
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			if config.Skipper(r) {
				return next(r)
			}

			originalWriter := r.ResponseWriter()
			buffer := new(bytes.Buffer)
			writer := &bodyDumpResponseWriter{
				Writer:         io.MultiWriter(originalWriter, buffer),
				ResponseWriter: originalWriter,
			}
			r.SetResponseWriter(writer)
			defer r.SetResponseWriter(originalWriter)

			err := next(r)
			status := r.StatusCode()
			if status >= http.StatusBadRequest {
				config.Handler(r, status, buffer.Bytes())
			}
			return err
		}
	}
}
