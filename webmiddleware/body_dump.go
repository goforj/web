package webmiddleware

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"

	"github.com/goforj/web"
)

// BodyDumpHandler receives the request and response payload.
type BodyDumpHandler func(web.Context, []byte, []byte)

// BodyDumpConfig configures request/response body dumping.
type BodyDumpConfig struct {
	Skipper Skipper
	Handler BodyDumpHandler
}

// DefaultBodyDumpConfig is the default body dump config.
var DefaultBodyDumpConfig = BodyDumpConfig{
	Skipper: DefaultSkipper,
}

// BodyDump captures request and response payloads.
// @group Middleware - Payloads
// Example:
// router := echoweb.New().Router()
//
//	router.Use(webmiddleware.BodyDump(func(c web.Context, reqBody, resBody []byte) {
//		log.Printf("%s %s -> %d bytes", c.Method(), c.URI(), len(resBody))
//	}))
//
//	router.POST("/webhooks", func(c web.Context) error {
//		return c.JSON(202, map[string]any{"queued": true})
//	})
func BodyDump(handler BodyDumpHandler) web.Middleware {
	config := DefaultBodyDumpConfig
	config.Handler = handler
	return BodyDumpWithConfig(config)
}

// BodyDumpWithConfig captures request and response payloads with config.
// @group Middleware - Payloads
// Example:
// router := echoweb.New().Router()
//
//	router.Use(webmiddleware.BodyDumpWithConfig(webmiddleware.BodyDumpConfig{
//		Skipper: func(c web.Context) bool {
//			return c.Path() == "/healthz"
//		},
//		Handler: func(c web.Context, reqBody, resBody []byte) {
//			log.Printf("%s %s -> %d bytes", c.Method(), c.URI(), len(resBody))
//		},
//	}))
func BodyDumpWithConfig(config BodyDumpConfig) web.Middleware {
	if config.Handler == nil {
		panic("web: body dump middleware requires a handler")
	}
	if config.Skipper == nil {
		config.Skipper = DefaultBodyDumpConfig.Skipper
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) (err error) {
			if config.Skipper(r) {
				return next(r)
			}

			req := r.Request()
			reqBody := []byte{}
			if req != nil && req.Body != nil {
				reqBody, err = io.ReadAll(req.Body)
				if err != nil {
					return err
				}
				req.Body = io.NopCloser(bytes.NewBuffer(reqBody))
				r.SetRequest(req)
			}

			resBody := new(bytes.Buffer)
			originalWriter := r.ResponseWriter()
			writer := &bodyDumpResponseWriter{
				Writer:         io.MultiWriter(originalWriter, resBody),
				ResponseWriter: originalWriter,
			}
			r.SetResponseWriter(writer)
			defer r.SetResponseWriter(originalWriter)

			err = next(r)
			config.Handler(r, reqBody, resBody.Bytes())
			return err
		}
	}
}

type bodyDumpResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

// WriteHeader captures the response status code before forwarding it.
func (w *bodyDumpResponseWriter) WriteHeader(code int) {
	w.ResponseWriter.WriteHeader(code)
}

// Write captures response bytes before forwarding them.
func (w *bodyDumpResponseWriter) Write(body []byte) (int, error) {
	return w.Writer.Write(body)
}

// Flush forwards flushing when the wrapped writer supports it.
func (w *bodyDumpResponseWriter) Flush() {
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err != nil && errors.Is(err, http.ErrNotSupported) {
		panic(errors.New("response writer flushing is not supported"))
	}
}

// Hijack forwards connection hijacking when the wrapped writer supports it.
func (w *bodyDumpResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// Unwrap returns the wrapped response writer.
func (w *bodyDumpResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
