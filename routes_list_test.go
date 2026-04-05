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

func TestBuildRouteEntriesSortsByPathThenMethod(t *testing.T) {
	entries := BuildRouteEntries([]RouteGroup{
		NewRouteGroup(
			"/api",
			[]Route{
				NewRoute("POST", "/users", testCreateRouteHandler),
				NewRoute("GET", "/users", testListRouteHandler),
				NewRoute("GET", "/users/:id", testShowRouteHandler),
			},
		),
	})

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Path != "/api/users" || strings.Join(entries[0].Methods, ",") != "GET" {
		t.Fatalf("expected GET /api/users first, got %#v", entries[0])
	}
	if entries[1].Path != "/api/users" || strings.Join(entries[1].Methods, ",") != "POST" {
		t.Fatalf("expected POST /api/users second, got %#v", entries[1])
	}
	if entries[2].Path != "/api/users/:id" || strings.Join(entries[2].Methods, ",") != "GET" {
		t.Fatalf("expected /api/users/:id last, got %#v", entries[2])
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

func testCreateRouteHandler(r Context) error { return nil }

func testListRouteHandler(r Context) error { return nil }

func testShowRouteHandler(r Context) error { return nil }
