package webmiddleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"sync"

	"github.com/goforj/web"
)

const (
	// GZIPEncoding is the gzip content-encoding value.
	GZIPEncoding = "gzip"
)

// Decompressor provides a pool of gzip readers.
type Decompressor interface {
	gzipDecompressPool() sync.Pool
}

// DefaultGzipDecompressPool is the default gzip reader pool.
type DefaultGzipDecompressPool struct{}

func (d *DefaultGzipDecompressPool) gzipDecompressPool() sync.Pool {
	return sync.Pool{New: func() any { return new(gzip.Reader) }}
}

// DecompressConfig configures request decompression.
type DecompressConfig struct {
	Skipper            Skipper
	GzipDecompressPool Decompressor
}

// DefaultDecompressConfig is the default decompress config.
var DefaultDecompressConfig = DecompressConfig{
	Skipper:            DefaultSkipper,
	GzipDecompressPool: &DefaultGzipDecompressPool{},
}

// Decompress decompresses gzip-encoded request bodies.
// @group Middleware - Compression
// Example:
// var body string
// compressed := &bytes.Buffer{}
// gz := gzip.NewWriter(compressed)
// _, _ = gz.Write([]byte("hello"))
// _ = gz.Close()
// req := httptest.NewRequest(http.MethodPost, "/", compressed)
// req.Header.Set("Content-Encoding", webmiddleware.GZIPEncoding)
// ctx := webtest.NewContext(req, nil, "/", nil)
// handler := webmiddleware.Decompress()(func(c web.Context) error {
// 	data, _ := io.ReadAll(c.Request().Body)
// 	body = string(data)
// 	return c.NoContent(http.StatusNoContent)
// })
// _ = handler(ctx)
// fmt.Println(body, ctx.Request().Header.Get("Content-Encoding"))
//	// hello
func Decompress() web.Middleware {
	return DecompressWithConfig(DefaultDecompressConfig)
}

// DecompressWithConfig decompresses gzip-encoded request bodies with config.
// @group Middleware - Compression
// Example:
// req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("plain"))
// ctx := webtest.NewContext(req, nil, "/", nil)
// handler := webmiddleware.DecompressWithConfig(webmiddleware.DecompressConfig{})(func(c web.Context) error {
// 	data, _ := io.ReadAll(c.Request().Body)
// 	fmt.Println(string(data))
// 	return nil
// })
// _ = handler(ctx)
//	// plain
func DecompressWithConfig(config DecompressConfig) web.Middleware {
	if config.Skipper == nil {
		config.Skipper = DefaultSkipper
	}
	if config.GzipDecompressPool == nil {
		config.GzipDecompressPool = DefaultDecompressConfig.GzipDecompressPool
	}

	return func(next web.Handler) web.Handler {
		pool := config.GzipDecompressPool.gzipDecompressPool()

		return func(r web.Context) error {
			if config.Skipper(r) {
				return next(r)
			}

			req := r.Request()
			if req == nil || req.Header.Get("Content-Encoding") != GZIPEncoding {
				return next(r)
			}

			item := pool.Get()
			reader, ok := item.(*gzip.Reader)
			if !ok || reader == nil {
				return r.JSON(http.StatusInternalServerError, map[string]any{
					"error": "invalid gzip reader",
				})
			}
			defer pool.Put(reader)

			body := req.Body
			defer body.Close()

			if err := reader.Reset(body); err != nil {
				if err == io.EOF {
					return next(r)
				}
				return err
			}
			defer reader.Close()

			req.Body = io.NopCloser(reader)
			req.Header.Del("Content-Encoding")
			r.SetRequest(req)

			return next(r)
		}
	}
}
