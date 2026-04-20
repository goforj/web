package echoweb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/goforj/web"
)

func TestNewServerRegistersMountsAndRoutes(t *testing.T) {
	server, err := NewServer(ServerConfig{
		RouteGroups: []web.RouteGroup{
			web.NewRouteGroup("/api", []web.Route{
				web.NewRoute(http.MethodGet, "/hello", func(r web.Context) error {
					return r.Text(http.StatusOK, "world")
				}),
			}),
		},
		Mounts: []web.RouterMount{
			func(router web.Router) error {
				router.GET("/-/health", func(r web.Context) error {
					return r.Text(http.StatusOK, "ok")
				})
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	testCases := []struct {
		path string
		body string
	}{
		{path: "/-/health", body: "ok"},
		{path: "/api/hello", body: "world"},
	}

	for _, testCase := range testCases {
		req := httptest.NewRequest(http.MethodGet, testCase.path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", testCase.path, rec.Code)
		}
		if rec.Body.String() != testCase.body {
			t.Fatalf("%s body = %q", testCase.path, rec.Body.String())
		}
	}
}

func TestServerNilAndLifecycleHelpers(t *testing.T) {
	var nilServer *Server

	if nilServer.Router() != nil {
		t.Fatal("nil server Router() should return nil")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	nilServer.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("nil server status = %d", rec.Code)
	}

	if err := nilServer.Serve(context.Background()); err != nil {
		t.Fatalf("nil server Serve() error = %v", err)
	}
}

func TestServerServeShutsDownOnContextCancel(t *testing.T) {
	server, err := NewServer(ServerConfig{
		Addr:            "127.0.0.1:0",
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	if server.Router() == nil {
		t.Fatal("Router() returned nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Serve() to stop")
	}
}
