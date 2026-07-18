package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/gorilla/mux"
	"github.com/julienschmidt/httprouter"
	"github.com/labstack/echo/v5"
)

const (
	staticTextScenario    benchmarkScenario = "static_text"
	pathParamJSONScenario benchmarkScenario = "path_param_json"
	middlewareScenario    benchmarkScenario = "middleware_chain"
	livePlainTextScenario benchmarkScenario = "live_plain_text"
	benchmarkContextKey                     = "benchmark-value"
	benchmarkContextValue                   = "ready"
	plainTextBody                           = "Hello, benchmark!"
)

type benchmarkScenario string

type benchmarkFramework struct {
	name string
	new  func(benchmarkScenario) http.Handler
}

type pathResponse struct {
	// ID carries the path parameter through each stack's JSON response API.
	ID string `json:"id"`
}

type requestContextKey struct{}

type benchmarkFailure struct {
	err error
}

type discardResponseWriter struct {
	header http.Header
	status int
	size   int
}

// BenchmarkHTTPStacks compares equivalent endpoint work across the supported
// Go HTTP stacks. Its slash-delimited names are a stable input to the benchmark
// SVG renderer.
func BenchmarkHTTPStacks(b *testing.B) {
	gin.SetMode(gin.ReleaseMode)

	scenarios := []benchmarkScenario{
		staticTextScenario,
		pathParamJSONScenario,
		middlewareScenario,
		livePlainTextScenario,
	}
	for _, scenario := range scenarios {
		scenario := scenario
		b.Run(string(scenario), func(b *testing.B) {
			for _, framework := range benchmarkFrameworks() {
				framework := framework
				b.Run(framework.name, func(b *testing.B) {
					handler := framework.new(scenario)
					if scenario == livePlainTextScenario {
						runLiveHTTPBenchmark(b, handler, scenario)
						return
					}
					runDispatchBenchmark(b, handler, scenario)
				})
			}
		})
	}
}

// TestHTTPStackContracts verifies that every measured handler satisfies the same semantic endpoint contract.
func TestHTTPStackContracts(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	scenarios := []benchmarkScenario{
		staticTextScenario,
		pathParamJSONScenario,
		middlewareScenario,
		livePlainTextScenario,
	}
	for _, scenario := range scenarios {
		t.Run(string(scenario), func(t *testing.T) {
			for _, framework := range benchmarkFrameworks() {
				t.Run(framework.name, func(t *testing.T) {
					handler := framework.new(scenario)
					request := httptest.NewRequest(http.MethodGet, benchmarkPath(scenario), nil)
					recorder := httptest.NewRecorder()
					handler.ServeHTTP(recorder, request)
					validateResponse(t, recorder.Code, recorder.Header(), recorder.Body.String(), scenario)
				})
			}
		})
	}
}

// benchmarkFrameworks returns the stacks in the measurement order selected for this process.
func benchmarkFrameworks() []benchmarkFramework {
	return rotateBenchmarkFrameworks(stableBenchmarkFrameworks(), benchmarkFrameworkRotation())
}

// stableBenchmarkFrameworks returns the canonical stack order used outside rotated measurements.
func stableBenchmarkFrameworks() []benchmarkFramework {
	return []benchmarkFramework{
		{name: "goforj_web", new: newGoForjWebHandler},
		{name: "net_http", new: newNetHTTPHandler},
		{name: "echo", new: newEchoHandler},
		{name: "gin", new: newGinHandler},
		{name: "chi", new: newChiHandler},
		{name: "gorilla_mux", new: newGorillaMuxHandler},
		{name: "httprouter", new: newHTTPRouterHandler},
	}
}

// benchmarkFrameworkRotation reads the deterministic rotation chosen by the measurement runner.
func benchmarkFrameworkRotation() int {
	value := strings.TrimSpace(os.Getenv(frameworkRotationEnvironment))
	if value == "" {
		return 0
	}
	rotation, err := strconv.Atoi(value)
	if err != nil {
		panic(fmt.Sprintf("%s must be an integer, got %q", frameworkRotationEnvironment, value))
	}
	return rotation
}

// rotateBenchmarkFrameworks moves each stack through different run-order positions without mutating the canonical slice.
func rotateBenchmarkFrameworks(frameworks []benchmarkFramework, rotation int) []benchmarkFramework {
	if len(frameworks) == 0 {
		return nil
	}
	rotation %= len(frameworks)
	if rotation < 0 {
		rotation += len(frameworks)
	}
	rotated := make([]benchmarkFramework, 0, len(frameworks))
	rotated = append(rotated, frameworks[rotation:]...)
	rotated = append(rotated, frameworks[:rotation]...)
	return rotated
}

// TestBenchmarkFrameworkRotation verifies deterministic rotation and canonical-order preservation.
func TestBenchmarkFrameworkRotation(t *testing.T) {
	t.Setenv(frameworkRotationEnvironment, "2")
	frameworks := benchmarkFrameworks()
	if got := frameworks[0].name; got != "echo" {
		t.Fatalf("first rotated framework = %q, want echo", got)
	}
	if got := frameworks[len(frameworks)-1].name; got != "net_http" {
		t.Fatalf("last rotated framework = %q, want net_http", got)
	}
	if got := stableBenchmarkFrameworks()[0].name; got != "goforj_web" {
		t.Fatalf("canonical first framework = %q, want goforj_web", got)
	}
}

// TestMiddlewareBaselineUsesSamePath keeps routing work out of the reported middleware delta.
func TestMiddlewareBaselineUsesSamePath(t *testing.T) {
	if got, want := benchmarkPath(middlewareScenario), benchmarkPath(staticTextScenario); got != want {
		t.Fatalf("middleware path = %q, want plaintext baseline path %q", got, want)
	}
}

// runDispatchBenchmark measures full in-process ServeHTTP dispatch without recorder allocations.
func runDispatchBenchmark(b *testing.B, handler http.Handler, scenario benchmarkScenario) {
	request := httptest.NewRequest(http.MethodGet, benchmarkPath(scenario), nil)
	validateDispatchResponse(b, handler, request, scenario)
	writer := newDiscardResponseWriter()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		writer.Reset()
		handler.ServeHTTP(writer, request)
	}
	b.StopTimer()
	reportRequestsPerSecond(b)
}

// runLiveHTTPBenchmark measures concurrent HTTP/1.1 loopback requests over reused connections.
func runLiveHTTPBenchmark(b *testing.B, handler http.Handler, scenario benchmarkScenario) {
	server := httptest.NewServer(handler)
	defer server.Close()

	maxConnections := runtime.GOMAXPROCS(0) * 4
	transport := &http.Transport{
		MaxIdleConns:        maxConnections,
		MaxIdleConnsPerHost: maxConnections,
		MaxConnsPerHost:     maxConnections,
		IdleConnTimeout:     time.Minute,
		DisableCompression:  true,
		ForceAttemptHTTP2:   false,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	target := server.URL + benchmarkPath(scenario)
	validateLiveResponse(b, client, target, scenario)
	template, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		b.Fatalf("create live benchmark request: %v", err)
	}

	var failure atomic.Pointer[benchmarkFailure]
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		request := template.Clone(context.Background())
		for pb.Next() {
			response, requestErr := client.Do(request)
			if requestErr != nil {
				failure.CompareAndSwap(nil, &benchmarkFailure{err: requestErr})
				return
			}
			_, copyErr := io.Copy(io.Discard, response.Body)
			closeErr := response.Body.Close()
			if copyErr != nil {
				failure.CompareAndSwap(nil, &benchmarkFailure{err: copyErr})
				return
			}
			if closeErr != nil {
				failure.CompareAndSwap(nil, &benchmarkFailure{err: closeErr})
				return
			}
		}
	})
	b.StopTimer()
	if result := failure.Load(); result != nil {
		b.Fatalf("live benchmark request: %v", result.err)
	}
	reportRequestsPerSecond(b)
}

// reportRequestsPerSecond adds a directly consumable throughput metric to benchmark output.
func reportRequestsPerSecond(b *testing.B) {
	elapsed := b.Elapsed()
	if elapsed <= 0 {
		return
	}
	b.ReportMetric(float64(b.N)/elapsed.Seconds(), "req/s")
}

// validateDispatchResponse checks the in-process contract before benchmark timing starts.
func validateDispatchResponse(b *testing.B, handler http.Handler, request *http.Request, scenario benchmarkScenario) {
	b.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	validateResponse(b, recorder.Code, recorder.Header(), recorder.Body.String(), scenario)
}

// validateLiveResponse checks the loopback HTTP contract before benchmark timing starts.
func validateLiveResponse(b *testing.B, client *http.Client, target string, scenario benchmarkScenario) {
	b.Helper()
	response, err := client.Get(target)
	if err != nil {
		b.Fatalf("validate live response: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		b.Fatalf("read live response: %v", readErr)
	}
	if closeErr != nil {
		b.Fatalf("close live response: %v", closeErr)
	}
	validateResponse(b, response.StatusCode, response.Header, string(body), scenario)
}

// validateResponse enforces equivalent endpoint semantics and middleware behavior across stacks.
func validateResponse(t testing.TB, status int, header http.Header, body string, scenario benchmarkScenario) {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("%s status = %d, want %d", scenario, status, http.StatusOK)
	}

	switch scenario {
	case staticTextScenario, middlewareScenario, livePlainTextScenario:
		mediaType, parameters, err := mime.ParseMediaType(header.Get("Content-Type"))
		if err != nil || mediaType != "text/plain" || !strings.EqualFold(parameters["charset"], "utf-8") {
			t.Fatalf("%s content type = %q, want text/plain with UTF-8 charset", scenario, header.Get("Content-Type"))
		}
		if body != plainTextBody {
			t.Fatalf("%s body = %q, want %q", scenario, body, plainTextBody)
		}
		if scenario == middlewareScenario && header.Get("X-Benchmark") != benchmarkContextValue {
			t.Fatalf("%s header = %q, want %q", scenario, header.Get("X-Benchmark"), benchmarkContextValue)
		}
	case pathParamJSONScenario:
		if !strings.HasPrefix(header.Get("Content-Type"), "application/json") {
			t.Fatalf("%s content type = %q, want application/json", scenario, header.Get("Content-Type"))
		}
		var payload pathResponse
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("%s decode response: %v", scenario, err)
		}
		if payload.ID != "42" {
			t.Fatalf("%s id = %q, want %q", scenario, payload.ID, "42")
		}
	default:
		t.Fatalf("unknown benchmark scenario %q", scenario)
	}
}

// benchmarkPath returns the request path shared by every stack for a scenario.
func benchmarkPath(scenario benchmarkScenario) string {
	switch scenario {
	case staticTextScenario, middlewareScenario:
		return "/benchmark/static"
	case pathParamJSONScenario:
		return "/benchmark/users/42"
	case livePlainTextScenario:
		return "/benchmark/live"
	default:
		panic(fmt.Sprintf("unknown benchmark scenario %q", scenario))
	}
}

// benchmarkRoutePath returns the registration path used by path-parameter routers.
func benchmarkRoutePath(scenario benchmarkScenario, parameter string) string {
	if scenario == pathParamJSONScenario {
		return "/benchmark/users/" + parameter
	}
	return benchmarkPath(scenario)
}

// newGoForjWebHandler builds the GoForj Web route for a benchmark scenario.
func newGoForjWebHandler(scenario benchmarkScenario) http.Handler {
	adapter := echoweb.New()
	router := adapter.Router()
	switch scenario {
	case staticTextScenario, livePlainTextScenario:
		router.GET(benchmarkPath(scenario), func(c web.Context) error {
			return c.Text(http.StatusOK, plainTextBody)
		})
	case pathParamJSONScenario:
		router.GET(benchmarkRoutePath(scenario, ":id"), func(c web.Context) error {
			return c.JSON(http.StatusOK, pathResponse{ID: c.Param("id")})
		})
	case middlewareScenario:
		router.Use(
			func(next web.Handler) web.Handler {
				return func(c web.Context) error {
					c.Set(benchmarkContextKey, benchmarkContextValue)
					return next(c)
				}
			},
			func(next web.Handler) web.Handler {
				return func(c web.Context) error {
					c.SetHeader("X-Benchmark", benchmarkContextValue)
					return next(c)
				}
			},
			func(next web.Handler) web.Handler {
				return func(c web.Context) error {
					if c.Get(benchmarkContextKey) != benchmarkContextValue {
						return c.Text(http.StatusInternalServerError, "missing middleware value")
					}
					return next(c)
				}
			},
		)
		router.GET(benchmarkPath(scenario), func(c web.Context) error {
			return c.Text(http.StatusOK, plainTextBody)
		})
	default:
		panic(fmt.Sprintf("unknown benchmark scenario %q", scenario))
	}
	return adapter
}

// newNetHTTPHandler builds the standard library ServeMux route for a benchmark scenario.
func newNetHTTPHandler(scenario benchmarkScenario) http.Handler {
	router := http.NewServeMux()
	switch scenario {
	case staticTextScenario, livePlainTextScenario:
		router.HandleFunc("GET "+benchmarkPath(scenario), func(w http.ResponseWriter, _ *http.Request) {
			writeText(w, http.StatusOK, plainTextBody)
		})
	case pathParamJSONScenario:
		router.HandleFunc("GET "+benchmarkRoutePath(scenario, "{id}"), func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, pathResponse{ID: r.PathValue("id")})
		})
	case middlewareScenario:
		router.HandleFunc("GET "+benchmarkPath(scenario), func(w http.ResponseWriter, _ *http.Request) {
			writeText(w, http.StatusOK, plainTextBody)
		})
		return chainHTTPMiddleware(router, setRequestValue, setResponseHeader, validateRequestValue)
	default:
		panic(fmt.Sprintf("unknown benchmark scenario %q", scenario))
	}
	return router
}

// newEchoHandler builds the native Echo route for a benchmark scenario.
func newEchoHandler(scenario benchmarkScenario) http.Handler {
	router := echo.New()
	switch scenario {
	case staticTextScenario, livePlainTextScenario:
		router.GET(benchmarkPath(scenario), func(c *echo.Context) error {
			return c.String(http.StatusOK, plainTextBody)
		})
	case pathParamJSONScenario:
		router.GET(benchmarkRoutePath(scenario, ":id"), func(c *echo.Context) error {
			return c.JSON(http.StatusOK, pathResponse{ID: c.Param("id")})
		})
	case middlewareScenario:
		router.Use(
			func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c *echo.Context) error {
					c.Set(benchmarkContextKey, benchmarkContextValue)
					return next(c)
				}
			},
			func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c *echo.Context) error {
					c.Response().Header().Set("X-Benchmark", benchmarkContextValue)
					return next(c)
				}
			},
			func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c *echo.Context) error {
					if c.Get(benchmarkContextKey) != benchmarkContextValue {
						return echo.NewHTTPError(http.StatusInternalServerError, "missing middleware value")
					}
					return next(c)
				}
			},
		)
		router.GET(benchmarkPath(scenario), func(c *echo.Context) error {
			return c.String(http.StatusOK, plainTextBody)
		})
	default:
		panic(fmt.Sprintf("unknown benchmark scenario %q", scenario))
	}
	return router
}

// newGinHandler builds the native Gin route for a benchmark scenario.
func newGinHandler(scenario benchmarkScenario) http.Handler {
	router := gin.New()
	switch scenario {
	case staticTextScenario, livePlainTextScenario:
		router.GET(benchmarkPath(scenario), func(c *gin.Context) {
			c.String(http.StatusOK, plainTextBody)
		})
	case pathParamJSONScenario:
		router.GET(benchmarkRoutePath(scenario, ":id"), func(c *gin.Context) {
			c.JSON(http.StatusOK, pathResponse{ID: c.Param("id")})
		})
	case middlewareScenario:
		router.Use(
			func(c *gin.Context) {
				c.Set(benchmarkContextKey, benchmarkContextValue)
				c.Next()
			},
			func(c *gin.Context) {
				c.Header("X-Benchmark", benchmarkContextValue)
				c.Next()
			},
			func(c *gin.Context) {
				value, exists := c.Get(benchmarkContextKey)
				if !exists || value != benchmarkContextValue {
					c.AbortWithStatus(http.StatusInternalServerError)
					return
				}
				c.Next()
			},
		)
		router.GET(benchmarkPath(scenario), func(c *gin.Context) {
			c.String(http.StatusOK, plainTextBody)
		})
	default:
		panic(fmt.Sprintf("unknown benchmark scenario %q", scenario))
	}
	return router
}

// newChiHandler builds the native Chi route for a benchmark scenario.
func newChiHandler(scenario benchmarkScenario) http.Handler {
	router := chi.NewRouter()
	switch scenario {
	case staticTextScenario, livePlainTextScenario:
		router.Get(benchmarkPath(scenario), func(w http.ResponseWriter, _ *http.Request) {
			writeText(w, http.StatusOK, plainTextBody)
		})
	case pathParamJSONScenario:
		router.Get(benchmarkRoutePath(scenario, "{id}"), func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, pathResponse{ID: chi.URLParam(r, "id")})
		})
	case middlewareScenario:
		router.Use(setRequestValue, setResponseHeader, validateRequestValue)
		router.Get(benchmarkPath(scenario), func(w http.ResponseWriter, _ *http.Request) {
			writeText(w, http.StatusOK, plainTextBody)
		})
	default:
		panic(fmt.Sprintf("unknown benchmark scenario %q", scenario))
	}
	return router
}

// newGorillaMuxHandler builds the native Gorilla Mux route for a benchmark scenario.
func newGorillaMuxHandler(scenario benchmarkScenario) http.Handler {
	router := mux.NewRouter()
	switch scenario {
	case staticTextScenario, livePlainTextScenario:
		router.HandleFunc(benchmarkPath(scenario), func(w http.ResponseWriter, _ *http.Request) {
			writeText(w, http.StatusOK, plainTextBody)
		}).Methods(http.MethodGet)
	case pathParamJSONScenario:
		router.HandleFunc(benchmarkRoutePath(scenario, "{id}"), func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, pathResponse{ID: mux.Vars(r)["id"]})
		}).Methods(http.MethodGet)
	case middlewareScenario:
		router.Use(setRequestValue, setResponseHeader, validateRequestValue)
		router.HandleFunc(benchmarkPath(scenario), func(w http.ResponseWriter, _ *http.Request) {
			writeText(w, http.StatusOK, plainTextBody)
		}).Methods(http.MethodGet)
	default:
		panic(fmt.Sprintf("unknown benchmark scenario %q", scenario))
	}
	return router
}

// newHTTPRouterHandler builds the native httprouter route for a benchmark scenario.
func newHTTPRouterHandler(scenario benchmarkScenario) http.Handler {
	router := httprouter.New()
	switch scenario {
	case staticTextScenario, livePlainTextScenario:
		router.GET(benchmarkPath(scenario), func(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
			writeText(w, http.StatusOK, plainTextBody)
		})
	case pathParamJSONScenario:
		router.GET(benchmarkRoutePath(scenario, ":id"), func(w http.ResponseWriter, _ *http.Request, params httprouter.Params) {
			writeJSON(w, http.StatusOK, pathResponse{ID: params.ByName("id")})
		})
	case middlewareScenario:
		router.GET(benchmarkPath(scenario), func(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
			writeText(w, http.StatusOK, plainTextBody)
		})
		return chainHTTPMiddleware(router, setRequestValue, setResponseHeader, validateRequestValue)
	default:
		panic(fmt.Sprintf("unknown benchmark scenario %q", scenario))
	}
	return router
}

// chainHTTPMiddleware applies standard-library middleware in declaration order.
func chainHTTPMiddleware(handler http.Handler, middleware ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		handler = middleware[i](handler)
	}
	return handler
}

// setRequestValue stores the value consumed by the third middleware stage.
func setRequestValue(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), requestContextKey{}, benchmarkContextValue)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// setResponseHeader writes an observable response header before dispatch continues.
func setResponseHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Benchmark", benchmarkContextValue)
		next.ServeHTTP(w, r)
	})
}

// validateRequestValue rejects requests that did not traverse the first middleware stage.
func validateRequestValue(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(requestContextKey{}) != benchmarkContextValue {
			http.Error(w, "missing middleware value", http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeText writes the plaintext contract used by routers without response helpers.
func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// writeJSON writes the standard-library JSON contract used by routing-only packages.
func writeJSON(w http.ResponseWriter, status int, payload pathResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// newDiscardResponseWriter creates a reusable writer outside the timed benchmark loop.
func newDiscardResponseWriter() *discardResponseWriter {
	return &discardResponseWriter{header: make(http.Header)}
}

// Header returns the reusable response header map.
func (w *discardResponseWriter) Header() http.Header {
	return w.header
}

// WriteHeader records the first response status, matching net/http semantics.
func (w *discardResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
}

// Write discards response bytes while retaining their count.
func (w *discardResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.size += len(body)
	return len(body), nil
}

// WriteString discards response text without forcing routing-only handlers through a byte conversion.
func (w *discardResponseWriter) WriteString(body string) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.size += len(body)
	return len(body), nil
}

// ReadFrom discards streamed response bytes without an intermediate buffer.
func (w *discardResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	size, err := io.Copy(io.Discard, reader)
	w.size += int(size)
	return size, err
}

// Reset clears per-response state while retaining allocated storage.
func (w *discardResponseWriter) Reset() {
	for name := range w.header {
		delete(w.header, name)
	}
	w.status = 0
	w.size = 0
}
