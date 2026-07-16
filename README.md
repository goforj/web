<p align="center">
  <img src="https://raw.githubusercontent.com/goforj/web/main/docs/assets/logo.png" width="300" alt="goforj/web logo">
</p>

<p align="center">
  App-facing HTTP contracts with an Echo-backed runtime for GoForj applications.
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/goforj/web"><img src="https://pkg.go.dev/badge/github.com/goforj/web.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
  <a href="https://github.com/goforj/web/actions/workflows/ci.yml"><img src="https://github.com/goforj/web/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/go-1.25%2B-blue?logo=go" alt="Go 1.25 or newer"></a>
  <a href="https://github.com/goforj/web/releases"><img src="https://img.shields.io/github/v/tag/goforj/web?label=version&sort=semver" alt="Latest release"></a>
  <a href="https://goreportcard.com/report/github.com/goforj/web"><img src="https://goreportcard.com/badge/github.com/goforj/web" alt="Go Report Card"></a>
  <a href="https://codecov.io/gh/goforj/web"><img src="https://codecov.io/gh/goforj/web/graph/badge.svg?token=Q0S6BVOM7R" alt="Coverage"></a>
</p>

`web` keeps application handlers behind focused `Context`, `Router`, and middleware contracts while Echo stays at the HTTP boundary. It includes an Echo adapter, route declarations, common middleware, WebSockets, Prometheus instrumentation, handler test helpers, and source-aware route and OpenAPI indexing.

The contracts deliberately expose less than Echo. Applications can stay on the smaller surface for ordinary HTTP work and use explicit Echo escape hatches when an integration needs the underlying engine or context.

## Installation

Requires Go 1.25 or newer.

```sh
go get github.com/goforj/web
```

## Quick start

```go
package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
)

func main() {
	adapter := echoweb.New()
	router := adapter.Router()

	router.Use(
		webmiddleware.Recover(),
		webmiddleware.RequestID(),
	)

	router.GET("/healthz", func(c web.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	router.GET("/users/:id", func(c web.Context) error {
		id := c.Param("id")
		return c.JSON(http.StatusOK, map[string]any{
			"id":   id,
			"name": fmt.Sprintf("user-%s", id),
		})
	})

	log.Fatal(http.ListenAndServe(":8080", adapter))
}
```

Start the server with `go run .`, then make requests from another shell:

```console
$ curl -s http://localhost:8080/healthz
ok
$ curl -s http://localhost:8080/users/42
{"id":"42","name":"user-42"}
```

The quick start owns the `http.Server` directly to keep the first example small. For cancellation-aware graceful shutdown, use [`echoweb.NewServer`](#graceful-server-lifecycle).

## Choose an entry point

| Start with | Use it when |
| --- | --- |
| `echoweb.New()` and `router.GET(...)` | Routes are registered directly and your application owns the `http.Server`. |
| `web.NewRouteGroup(...)` and `web.RegisterRoutes(...)` | Routes should be reusable declarations for reporting, indexing, or generated application composition. |
| `echoweb.NewServer(...)` | The adapter should register route groups and own graceful HTTP shutdown. |
| `echoweb.Wrap(engine)` | An existing Echo engine needs to expose the app-facing `web.Router` contract. |

## Package map

| Package | Purpose |
| --- | --- |
| [`web`](https://pkg.go.dev/github.com/goforj/web) | Handler, context, router, route declaration, WebSocket, and route-reporting contracts. |
| [`adapter/echoweb`](https://pkg.go.dev/github.com/goforj/web/adapter/echoweb) | Echo adapter, native escape hatches, and server lifecycle. |
| [`webmiddleware`](https://pkg.go.dev/github.com/goforj/web/webmiddleware) | Authentication, security, routing, payload, compression, timeout, proxy, and rate-limit middleware. |
| [`webprometheus`](https://pkg.go.dev/github.com/goforj/web/webprometheus) | HTTP metrics middleware, scrape handlers, and Pushgateway support. |
| [`webindex`](https://pkg.go.dev/github.com/goforj/web/webindex) | Source-aware route manifests, diagnostics, schemas, and OpenAPI documents. |
| [`webtest`](https://pkg.go.dev/github.com/goforj/web/webtest) | Lightweight contexts for isolated handler tests. |

## Common workflows

The quick start above is a complete program. The following recipes are focused excerpts; complete generated programs are available in [`examples`](examples).

### Declarative route groups

Route values keep registration data available for route reporting and source-aware tooling instead of burying every route in adapter calls.

```go
adapter := echoweb.New()

routes := []web.Route{
	web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error {
		return c.NoContent(http.StatusNoContent)
	}),
	web.NewRoute(http.MethodGet, "/users", func(c web.Context) error {
		return c.JSON(http.StatusOK, []map[string]any{{"id": 1}})
	}),
}

group := web.NewRouteGroup("/api", routes)
if err := web.RegisterRoutes(adapter.Router(), []web.RouteGroup{group}); err != nil {
	log.Fatal(err)
}
```

### Middleware phases

Use `Pre` for middleware that must change the request method or path before route matching. Method override, rewrite, and trailing-slash middleware belong in this phase. Use `Use` for middleware that wraps the matched request handler.

```go
router := echoweb.New().Router()

router.Pre(
	webmiddleware.MethodOverride(),
	webmiddleware.RemoveTrailingSlash(),
)

store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
router.Use(
	webmiddleware.Recover(),
	webmiddleware.RequestID(),
	webmiddleware.RateLimiter(store),
)
```

Middleware runs in registration order, so put recovery and request identity near the outside of the chain and keep a shared rate-limit store for the lifetime of the application.

### Graceful server lifecycle

`Server.Serve` listens until its context is cancelled, then shuts down with the configured timeout.

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

server, err := echoweb.NewServer(echoweb.ServerConfig{
	Addr: ":8080",
	RouteGroups: []web.RouteGroup{
		web.NewRouteGroup("/api", []web.Route{
			web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error {
				return c.NoContent(http.StatusNoContent)
			}),
		}),
	},
	ShutdownTimeout: 10 * time.Second,
})
if err != nil {
	log.Fatal(err)
}
if err := server.Serve(ctx); err != nil {
	log.Fatal(err)
}
```

### Test a handler

`webtest.NewContext` runs an isolated handler without booting a router or listener. Use the Echo adapter with `httptest` when the route mapping itself is part of the behavior under test.

```go
func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	ctx := webtest.NewContext(req, rec, "/healthz", nil)

	handler := func(c web.Context) error {
		return c.Text(http.StatusOK, "ok")
	}
	if err := handler(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("expected ok, got %q", rec.Body.String())
	}
}
```

### Expose Prometheus metrics

Use one metrics instance for both middleware and scraping. An explicit registry keeps collector ownership local to the application.

```go
registry := prometheus.NewRegistry()
metrics := webprometheus.MustNew(webprometheus.Config{
	Namespace:  "app",
	Registerer: registry,
	Gatherer:   registry,
})

router := echoweb.New().Router()
router.Use(metrics.Middleware())
router.GET("/metrics", metrics.Handler())
router.GET("/users", func(c web.Context) error {
	return c.JSON(http.StatusOK, []map[string]any{{"id": 1}})
})
```

### Generate a route manifest and OpenAPI document

Set `RouteCompositionPath` to the source file that assembles the application route groups. Scoping the index to that composition keeps unrelated fixtures and unused providers out of the published contract.

```go
manifest, err := webindex.Run(context.Background(), webindex.IndexOptions{
	Root:                 ".",
	RouteCompositionPath: "internal/http/routes.go",
	OutPath:              "build/webindex.json",
	DiagnosticsPath:      "build/webindex.diagnostics.json",
	OpenAPIPath:           "build/openapi.json",
	Strict:                true,
})
if err != nil {
	log.Fatal(err)
}
log.Printf("indexed %d operations", len(manifest.Operations))
```

`Strict` promotes unresolved source evidence into an error instead of publishing an ambiguous contract. The returned manifest still contains structured diagnostics for reporting.

### Add a WebSocket route

WebSocket handlers use the same app-facing context plus a small connection contract.

```go
router := echoweb.New().Router()
router.GETWS("/ws", func(c web.Context, conn web.WebSocketConn) error {
	var message map[string]any
	if err := conn.ReadJSON(&message); err != nil {
		return err
	}
	return conn.WriteJSON(map[string]any{"echo": message})
})
```

## Echo escape hatches

Use [`echoweb.Wrap`](https://pkg.go.dev/github.com/goforj/web/adapter/echoweb#Wrap) to adapt an existing Echo engine. `Adapter.Echo`, `echoweb.UnwrapContext`, and `Context.Native` expose the underlying implementation when a framework-specific integration genuinely needs it.

If an application only needs Echo and benefits from its full native API everywhere, using Echo directly is reasonable. `web` earns its place when the smaller handler contract, route declarations, shared middleware surface, testing helpers, metrics, or source-aware indexing are useful application boundaries.

## Client IP trust

For backward compatibility, the Echo adapter initializes Echo's legacy IP extractor when an engine does not already have one. That extractor trusts forwarding headers without proxy checks, so configure it deliberately before using `Context.RealIP` for security, rate limiting, or auditing.

For a server reached directly by clients, use the network peer address:

```go
adapter := echoweb.New()
adapter.Echo().IPExtractor = echo.ExtractIPDirect()
```

Behind a trusted proxy, configure `echo.ExtractIPFromXFFHeader` or `echo.ExtractIPFromRealIPHeader` with trust options that match the deployment, and ensure the edge proxy removes client-supplied forwarding headers before adding its own.

## Reference

<!-- api:embed:start -->
The [complete API documentation](https://pkg.go.dev/github.com/goforj/web) includes every exported type, field, function, and method.

The [generated API examples](API.md) collect the functions and methods annotated with runnable examples in this repository.
<!-- api:embed:end -->
