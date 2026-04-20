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
// @group Middleware
// Example:
// _ = webmiddleware.Decompress()
//	// true
func Decompress() web.Middleware {
	return DecompressWithConfig(DefaultDecompressConfig)
}

// DecompressWithConfig decompresses gzip-encoded request bodies with config.
// @group Middleware
// Example:
// _ = webmiddleware.DecompressWithConfig(webmiddleware.DecompressConfig{})
//	// true
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
