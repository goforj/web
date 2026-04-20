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

func TestBuildRouteEntriesIncludesWebSocketRoutes(t *testing.T) {
	entries := BuildRouteEntries([]RouteGroup{
		NewRouteGroup(
			"/api",
			[]Route{
				NewWebSocketRoute("/stream", testWebSocketRouteHandler),
			},
		),
	})

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if got := strings.Join(entries[0].Methods, ","); got != "GETWS" {
		t.Fatalf("expected GETWS method, got %q", got)
	}
	if entries[0].Path != "/api/stream" {
		t.Fatalf("expected websocket route path, got %q", entries[0].Path)
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

func TestRouteListHelperFunctions(t *testing.T) {
	t.Run("normalize methods and colorization", func(t *testing.T) {
		all := append([]string(nil), allHTTPMethods...)
		if got := normalizeMethods(all); got != "ALL" {
			t.Fatalf("normalizeMethods(all) = %q", got)
		}
		notAll := append([]string(nil), allHTTPMethods...)
		notAll[len(notAll)-1] = "CUSTOM"
		if got := normalizeMethods(notAll); got == "ALL" {
			t.Fatalf("normalizeMethods(notAll) = %q", got)
		}
		if got := normalizeMethods([]string{"POST", "GET", "GET"}); got != "GET, POST" {
			t.Fatalf("normalizeMethods(mixed) = %q", got)
		}
		if got := colorize(ansiCell, ""); got != "" {
			t.Fatalf("colorize(\"\") = %q", got)
		}
		if got := colorizeMethod("GET"); !strings.Contains(got, ansiGet) {
			t.Fatalf("colorizeMethod(GET) = %q", got)
		}
		if got := colorizeMethod("GETWS"); !strings.Contains(got, ansiGet) {
			t.Fatalf("colorizeMethod(GETWS) = %q", got)
		}
		if got := colorizeMethod("POST"); !strings.Contains(got, ansiPost) {
			t.Fatalf("colorizeMethod(POST) = %q", got)
		}
		if got := colorizeMethod("PUT"); !strings.Contains(got, ansiPut) {
			t.Fatalf("colorizeMethod(PUT) = %q", got)
		}
		if got := colorizeMethod("PATCH"); !strings.Contains(got, ansiPatch) {
			t.Fatalf("colorizeMethod(PATCH) = %q", got)
		}
		if got := colorizeMethod("DELETE"); !strings.Contains(got, ansiDelete) {
			t.Fatalf("colorizeMethod(DELETE) = %q", got)
		}
		if got := colorizeMethod("TRACE"); !strings.Contains(got, ansiCell) {
			t.Fatalf("colorizeMethod(TRACE) = %q", got)
		}
		if got := colorizeMiddleware("auth"); !strings.Contains(got, ansiMiddleware) {
			t.Fatalf("colorizeMiddleware(auth) = %q", got)
		}
	})

	t.Run("middleware shortcode helpers", func(t *testing.T) {
		entry := &RouteEntry{Middlewares: []string{
			"webmiddleware.RequestID",
			strings.Repeat("webmiddleware.ReallyLongMiddlewareName", 2),
		}}
		if !shouldUseMiddlewareShortcodes([]*RouteEntry{entry}) {
			t.Fatal("shouldUseMiddlewareShortcodes() should enable legend for long middleware cells")
		}
		if shouldUseMiddlewareShortcodes([]*RouteEntry{{Middlewares: []string{"mw"}}}) {
			t.Fatal("shouldUseMiddlewareShortcodes() should keep short middleware inline")
		}
		codeToName, nameToCode := buildMiddlewareShortcodes([]*RouteEntry{entry})
		if len(codeToName) != 2 || len(nameToCode) != 2 {
			t.Fatalf("shortcodes = %#v %#v", codeToName, nameToCode)
		}
		cfg := middlewareRenderConfig{useShortcodes: true, nameToCode: nameToCode}
		rendered := renderMiddlewareCell(entry.Middlewares, cfg)
		if strings.Contains(rendered, "ReallyLongMiddlewareName") {
			t.Fatalf("renderMiddlewareCell() should use shortcode, got %q", rendered)
		}
		if got := renderMiddlewareCell([]string{"mw"}, middlewareRenderConfig{}); got != "mw" {
			t.Fatalf("renderMiddlewareCell(default) = %q", got)
		}
	})

	t.Run("name helpers", func(t *testing.T) {
		if got := friendlyMiddlewareCode("webmiddleware.RequestID"); got == "" {
			t.Fatal("friendlyMiddlewareCode() returned empty code")
		}
		if got := friendlyMiddlewareCode("requestOnly"); got != "O" {
			t.Fatalf("friendlyMiddlewareCode(single) = %q", got)
		}
		if got := friendlyMiddlewareCode("."); got != "MW" {
			t.Fatalf("friendlyMiddlewareCode(empty) = %q", got)
		}
		if got := uppercaseHint("RequestID"); got != "RID" {
			t.Fatalf("uppercaseHint(RequestID) = %q", got)
		}
		if got := uppercaseHint("HTTPRequestWriter"); got != "HTTP" {
			t.Fatalf("uppercaseHint(HTTPRequestWriter) = %q", got)
		}
		if got := uppercaseHint("request"); got != "R" {
			t.Fatalf("uppercaseHint(request) = %q", got)
		}
		if got := uppercaseHint(""); got != "" {
			t.Fatalf("uppercaseHint(empty) = %q", got)
		}
		pkg, fn := splitMiddlewareName("webmiddleware.RequestID")
		if pkg != "webmiddleware" || fn != "RequestID" {
			t.Fatalf("splitMiddlewareName() = (%q, %q)", pkg, fn)
		}
		pkg, fn = splitMiddlewareName("single")
		if pkg != "single" || fn != "" {
			t.Fatalf("splitMiddlewareName(single) = (%q, %q)", pkg, fn)
		}
		if got := fnvSuffix("demo", 1); got == 0 {
			t.Fatal("fnvSuffix() should produce a non-zero byte")
		}
	})
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

func testWebSocketRouteHandler(r Context, conn WebSocketConn) error { return nil }
