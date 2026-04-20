package echoweb

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/goforj/web"
)

// ServerConfig configures an Echo-backed web server.
type ServerConfig struct {
	Addr            string
	RouteGroups     []web.RouteGroup
	Mounts          []web.RouterMount
	ShutdownTimeout time.Duration
}

// Server owns adapter bootstrap plus HTTP lifecycle management.
type Server struct {
	adapter         *Adapter
	httpServer      *http.Server
	shutdownTimeout time.Duration
}

// NewServer creates an Echo-backed server from web route groups and mounts.
// @group Adapter
// Example:
// server, err := echoweb.NewServer(echoweb.ServerConfig{
// 	RouteGroups: []web.RouteGroup{
// 		web.NewRouteGroup("/api", []web.Route{
// 			web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return c.NoContent(http.StatusOK) }),
// 		}),
// 	},
// })
// _ = server
// fmt.Println(err == nil)
//	// true
func NewServer(config ServerConfig) (*Server, error) {
	adapter := New()
	router := adapter.Router()
	shutdownTimeout := config.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = 30 * time.Second
	}

	if err := web.MountRouter(router, config.Mounts); err != nil {
		return nil, err
	}
	if err := web.RegisterRoutes(router, config.RouteGroups); err != nil {
		return nil, err
	}

	return &Server{
		adapter: adapter,
		httpServer: &http.Server{
			Addr:    config.Addr,
			Handler: adapter,
		},
		shutdownTimeout: shutdownTimeout,
	}, nil
}

// Router exposes the app-facing router contract.
// @group Adapter
// Example:
// server, _ := echoweb.NewServer(echoweb.ServerConfig{})
// _ = server.Router()
func (s *Server) Router() web.Router {
	if s == nil || s.adapter == nil {
		return nil
	}
	return s.adapter.Router()
}

// ServeHTTP exposes the server as an http.Handler for tests and local probing.
// @group Adapter
// Example:
// server, _ := echoweb.NewServer(echoweb.ServerConfig{
// 	RouteGroups: []web.RouteGroup{
// 		web.NewRouteGroup("/api", []web.Route{
// 			web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return c.NoContent(http.StatusOK) }),
// 		}),
// 	},
// })
// rr := httptest.NewRecorder()
// req := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
// server.ServeHTTP(rr, req)
// fmt.Println(rr.Code)
//	// 204
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.httpServer == nil {
		http.NotFound(w, r)
		return
	}
	s.httpServer.Handler.ServeHTTP(w, r)
}

// Serve starts the server and gracefully shuts it down when ctx is cancelled.
// @group Adapter
// Example:
// server, _ := echoweb.NewServer(echoweb.ServerConfig{Addr: "127.0.0.1:0"})
// ctx, cancel := context.WithCancel(context.Background())
// cancel()
// fmt.Println(server.Serve(ctx) == nil)
//	// true
func (s *Server) Serve(ctx context.Context) error {
	if s == nil || s.httpServer == nil {
		return nil
	}

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- s.httpServer.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()

		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}

		err := <-serverErr
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}

		return nil
	}
}
