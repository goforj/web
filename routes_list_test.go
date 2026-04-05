package web

import (
	"strings"
	"testing"
)

func TestBuildRouteEntriesMergesMethodsAndMiddlewares(t *testing.T) {
	entries := BuildRouteEntries([]RouteGroup{
		NewRouteGroup(
			"/api",
			[]Route{
				NewRoute("GET", "/users", testRouteHandler, testRouteMiddleware).WithMiddlewareNames("web.testRouteMiddleware"),
				NewRoute("POST", "/users", testRouteHandler, testRouteMiddleware).WithMiddlewareNames("web.testRouteMiddleware"),
			},
			testGroupMiddleware,
		).WithMiddlewareNames("web.testGroupMiddleware"),
	})

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Path != "/api/users" {
		t.Fatalf("expected merged path, got %q", entry.Path)
	}
	if strings.Join(entry.Methods, ",") != "GET,POST" {
		t.Fatalf("expected merged methods, got %#v", entry.Methods)
	}
	if len(entry.Middlewares) != 2 {
		t.Fatalf("expected group and route middleware, got %#v", entry.Middlewares)
	}
}

func TestBuildRouteEntriesIncludesExtraRoutes(t *testing.T) {
	entries := BuildRouteEntries(nil, RouteEntry{
		Path:    "/-/health",
		Handler: "http.Server.healthStatus",
		Methods: []string{"GET"},
	})
	if len(entries) != 1 || entries[0].Path != "/-/health" {
		t.Fatalf("expected extra entry, got %#v", entries)
	}
}

func TestRenderRouteTableIncludesTitleAndLegend(t *testing.T) {
	table := RenderRouteTable([]RouteEntry{
		{
			Path:        "/api/users",
			Handler:     "users.List",
			Methods:     []string{"GET", "POST"},
			Middlewares: []string{strings.Repeat("middleware.LongMiddlewareName", 3)},
		},
	})
	if !strings.Contains(table, "API Routes") {
		t.Fatalf("expected title in table, got %q", table)
	}
	if !strings.Contains(table, "Middleware Legend") {
		t.Fatalf("expected legend in table, got %q", table)
	}
}

func testRouteHandler(r Context) error { return nil }

func testGroupMiddleware(next Handler) Handler {
	return func(r Context) error { return next(r) }
}

func testRouteMiddleware(next Handler) Handler {
	return func(r Context) error { return next(r) }
}
