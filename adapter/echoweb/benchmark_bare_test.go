package echoweb

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/goforj/web"
	echo "github.com/labstack/echo/v5"
)

func BenchmarkEchoBareHandler(b *testing.B) {
	b.Run("text", benchmarkEchoBareText)
	b.Run("json", benchmarkEchoBareJSON)
}

func BenchmarkWebBareHandler(b *testing.B) {
	b.Run("text", benchmarkWebBareText)
	b.Run("json", benchmarkWebBareJSON)
}

func benchmarkEchoBareText(b *testing.B) {
	engine := echo.New()
	req := newBenchmarkRequest(http.MethodGet, "/plain")
	writer := &benchmarkResponseWriter{}
	ctx := echo.NewContext(req, writer, engine)
	handler := func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	}

	runBareEchoBenchmark(b, ctx, writer, req, handler)
}

func benchmarkWebBareText(b *testing.B) {
	engine := echo.New()
	req := newBenchmarkRequest(http.MethodGet, "/plain")
	writer := &benchmarkResponseWriter{}
	ctx := echo.NewContext(req, writer, engine)
	handler := adaptHandler(func(c web.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	runBareEchoBenchmark(b, ctx, writer, req, handler)
}

func benchmarkEchoBareJSON(b *testing.B) {
	engine := echo.New()
	req := newBenchmarkRequest(http.MethodGet, "/users/42")
	writer := &benchmarkResponseWriter{}
	ctx := echo.NewContext(req, writer, engine)
	handler := func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"id":     "42",
			"method": req.Method,
		})
	}

	runBareEchoBenchmark(b, ctx, writer, req, handler)
}

func benchmarkWebBareJSON(b *testing.B) {
	engine := echo.New()
	req := newBenchmarkRequest(http.MethodGet, "/users/42")
	writer := &benchmarkResponseWriter{}
	ctx := echo.NewContext(req, writer, engine)
	handler := adaptHandler(func(c web.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"id":     "42",
			"method": c.Method(),
		})
	})

	runBareEchoBenchmark(b, ctx, writer, req, handler)
}

func runBareEchoBenchmark(b *testing.B, ctx *echo.Context, writer *benchmarkResponseWriter, req *http.Request, handler echo.HandlerFunc) {
	b.ReportAllocs()
	b.ResetTimer()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		writer.Reset()
		ctx.Reset(req, writer)
		if err := handler(ctx); err != nil {
			b.Fatalf("handler error: %v", err)
		}
		if writer.status != http.StatusOK {
			b.Fatalf("status = %d", writer.status)
		}
	}
	reportRequestsPerSecond(b, start)
}

func newBenchmarkRequest(method string, target string) *http.Request {
	req, err := http.NewRequest(method, target, nil)
	if err != nil {
		panic(err)
	}
	return req
}

type benchmarkResponseWriter struct {
	header http.Header
	status int
	size   int
}

func (w *benchmarkResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *benchmarkResponseWriter) WriteHeader(status int) {
	w.status = status
}

func (w *benchmarkResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.size += len(p)
	return len(p), nil
}

func (w *benchmarkResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := io.Copy(io.Discard, r)
	w.size += int(n)
	return n, err
}

func (w *benchmarkResponseWriter) Reset() {
	for key := range w.header {
		delete(w.header, key)
	}
	w.status = 0
	w.size = 0
}
