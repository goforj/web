package echoweb

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
