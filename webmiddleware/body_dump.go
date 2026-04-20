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
// @group Middleware
// Example:
// mw := webmiddleware.BodyDump(func(c web.Context, reqBody, resBody []byte) {})
// _ = mw
//	// true
func BodyDump(handler BodyDumpHandler) web.Middleware {
	config := DefaultBodyDumpConfig
	config.Handler = handler
	return BodyDumpWithConfig(config)
}

// BodyDumpWithConfig captures request and response payloads with config.
// @group Middleware
// Example:
// mw := webmiddleware.BodyDumpWithConfig(webmiddleware.BodyDumpConfig{
// 	Handler: func(c web.Context, reqBody, resBody []byte) {},
// })
// _ = mw
//	// true
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

func (w *bodyDumpResponseWriter) WriteHeader(code int) {
	w.ResponseWriter.WriteHeader(code)
}

func (w *bodyDumpResponseWriter) Write(body []byte) (int, error) {
	return w.Writer.Write(body)
}

func (w *bodyDumpResponseWriter) Flush() {
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err != nil && errors.Is(err, http.ErrNotSupported) {
		panic(errors.New("response writer flushing is not supported"))
	}
}

func (w *bodyDumpResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *bodyDumpResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
