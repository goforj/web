package webmiddleware

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/goforj/web"
)

const gzipScheme = "gzip"

// GzipConfig configures gzip compression.
type GzipConfig struct {
	Skipper   Skipper
	Level     int
	MinLength int
}

// DefaultGzipConfig is the default gzip config.
var DefaultGzipConfig = GzipConfig{
	Skipper:   DefaultSkipper,
	Level:     -1,
	MinLength: 0,
}

// Gzip enables gzip response compression for clients that support it.
// @group Middleware - Compression
// Example:
// router := echoweb.New().Router()
//
//	router.GET("/feed", func(c web.Context) error {
//		return c.Text(200, "large feed response")
//	}, webmiddleware.Gzip())
func Gzip() web.Middleware {
	return GzipWithConfig(DefaultGzipConfig)
}

// Compress enables gzip response compression for clients that support it.
// @group Middleware - Compression
// Example:
// router := echoweb.New().Router()
// router.Use(webmiddleware.Compress())
//
//	router.GET("/reports", func(c web.Context) error {
//		return c.Text(200, "large report response")
//	})
func Compress() web.Middleware {
	return Gzip()
}

// GzipWithConfig enables gzip response compression with custom options.
// @group Middleware - Compression
// Example:
// router := echoweb.New().Router()
//
//	router.Use(webmiddleware.GzipWithConfig(webmiddleware.GzipConfig{
//		MinLength: 1024,
//	}))
func GzipWithConfig(config GzipConfig) web.Middleware {
	if config.Skipper == nil {
		config.Skipper = DefaultGzipConfig.Skipper
	}
	if config.Level == 0 {
		config.Level = DefaultGzipConfig.Level
	}
	if config.MinLength < 0 {
		config.MinLength = DefaultGzipConfig.MinLength
	}

	pool := gzipCompressPool(config)
	bufferPool := gzipBufferPool()

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			if config.Skipper(r) {
				return next(r)
			}

			req := r.Request()
			if req == nil {
				return next(r)
			}

			r.AddHeader("Vary", "Accept-Encoding")
			if !strings.Contains(req.Header.Get("Accept-Encoding"), gzipScheme) {
				return next(r)
			}

			item := pool.Get()
			writer, ok := item.(*gzip.Writer)
			if !ok {
				return r.JSON(http.StatusInternalServerError, map[string]any{"error": "invalid gzip writer"})
			}

			originalWriter := r.ResponseWriter()
			writer.Reset(originalWriter)

			buffer := bufferPool.Get().(*bytes.Buffer)
			buffer.Reset()

			gzw := &gzipResponseWriter{
				Writer:         writer,
				ResponseWriter: originalWriter,
				minLength:      config.MinLength,
				buffer:         buffer,
			}
			r.SetResponseWriter(gzw)

			defer func() {
				if !gzw.wroteBody {
					r.SetResponseWriter(originalWriter)
					writer.Reset(io.Discard)
				} else if !gzw.minLengthExceeded {
					r.SetResponseWriter(originalWriter)
					if gzw.wroteHeader {
						originalWriter.WriteHeader(gzw.code)
					}
					_, _ = gzw.buffer.WriteTo(originalWriter)
					writer.Reset(io.Discard)
				}
				_ = writer.Close()
				bufferPool.Put(buffer)
				pool.Put(writer)
			}()

			return next(r)
		}
	}
}

type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
	wroteHeader       bool
	wroteBody         bool
	minLength         int
	minLengthExceeded bool
	buffer            *bytes.Buffer
	code              int
}

// WriteHeader marks the response as compressed before writing the status code.
func (w *gzipResponseWriter) WriteHeader(code int) {
	w.Header().Del("Content-Length")
	w.wroteHeader = true
	w.code = code
}

// Write compresses response bytes before forwarding them.
func (w *gzipResponseWriter) Write(body []byte) (int, error) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", http.DetectContentType(body))
	}
	w.wroteBody = true

	if !w.minLengthExceeded {
		n, err := w.buffer.Write(body)
		if w.buffer.Len() >= w.minLength {
			w.minLengthExceeded = true
			w.Header().Set("Content-Encoding", gzipScheme)
			if w.wroteHeader {
				w.ResponseWriter.WriteHeader(w.code)
			}
			return w.Writer.Write(w.buffer.Bytes())
		}
		return n, err
	}

	return w.Writer.Write(body)
}

// Flush flushes the gzip stream and then the underlying writer when possible.
func (w *gzipResponseWriter) Flush() {
	if !w.minLengthExceeded {
		w.minLengthExceeded = true
		w.Header().Set("Content-Encoding", gzipScheme)
		if w.wroteHeader {
			w.ResponseWriter.WriteHeader(w.code)
		}
		_, _ = w.Writer.Write(w.buffer.Bytes())
	}

	if gw, ok := w.Writer.(*gzip.Writer); ok {
		_ = gw.Flush()
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap returns the wrapped response writer.
func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Hijack forwards connection hijacking when the wrapped writer supports it.
func (w *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// Push forwards HTTP/2 server pushes when the wrapped writer supports them.
func (w *gzipResponseWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

// gzipCompressPool returns the gzip writer pool for one middleware config.
func gzipCompressPool(config GzipConfig) sync.Pool {
	return sync.Pool{
		New: func() any {
			writer, err := gzip.NewWriterLevel(io.Discard, config.Level)
			if err != nil {
				return nil
			}
			return writer
		},
	}
}

// gzipBufferPool returns the buffer pool used by compressed responses.
func gzipBufferPool() sync.Pool {
	return sync.Pool{
		New: func() any { return &bytes.Buffer{} },
	}
}
