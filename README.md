<p align="center">
  <strong>web</strong>
</p>

<p align="center">
  Minimal app-facing HTTP abstractions, middleware, adapters, and route indexing for GoForj.
</p>

<p align="center">
<!-- test-count:embed:start -->
<img src="https://img.shields.io/badge/unit_tests-105-brightgreen" alt="Unit tests (executed count)">
<img src="https://img.shields.io/badge/integration_tests-0-blue" alt="Integration tests (executed count)">
<!-- test-count:embed:end -->
</p>

## Installation

```bash
go get github.com/goforj/web
```

## Quick Start

```go
package main

import (
	"net/http"

	"github.com/goforj/web"
	"github.com/goforj/web/webtest"
)

func main() {
	handler := web.Handler(func(c web.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	ctx := webtest.NewContext(nil, nil, "/healthz", nil)
	_ = handler(ctx)
}
```

## Packages

- `web`: app-facing interfaces and route registration helpers
- `webmiddleware`: reusable HTTP middleware
- `adapter/echoweb`: Echo-backed adapter implementation
- `webprometheus`: Prometheus middleware and scrape handler
- `webindex`: route and OpenAPI index generation
- `webtest`: lightweight handler testing context

## API

<!-- api:embed:start -->
## API Index

| Group | Functions |
|------:|:-----------|
| **Adapter** | [Adapter.Echo](#echoweb-adapter-echo) [Adapter.Router](#echoweb-adapter-router) [Adapter.ServeHTTP](#echoweb-adapter-servehttp) [New](#echoweb-new) [NewServer](#echoweb-newserver) [Server.Router](#echoweb-server-router) [Server.Serve](#echoweb-server-serve) [Server.ServeHTTP](#echoweb-server-servehttp) [UnwrapContext](#echoweb-unwrapcontext) [UnwrapWebSocketConn](#echoweb-unwrapwebsocketconn) [Wrap](#echoweb-wrap) |
| **Indexing** | [Run](#webindex-run) |
| **Middleware** | [AddTrailingSlash](#webmiddleware-addtrailingslash) [AddTrailingSlashWithConfig](#webmiddleware-addtrailingslashwithconfig) [BasicAuth](#webmiddleware-basicauth) [BasicAuthWithConfig](#webmiddleware-basicauthwithconfig) [BodyDump](#webmiddleware-bodydump) [BodyDumpWithConfig](#webmiddleware-bodydumpwithconfig) [BodyLimit](#webmiddleware-bodylimit) [BodyLimitWithConfig](#webmiddleware-bodylimitwithconfig) [CORS](#webmiddleware-cors) [CORSWithConfig](#webmiddleware-corswithconfig) [CSRF](#webmiddleware-csrf) [CSRFWithConfig](#webmiddleware-csrfwithconfig) [Compress](#webmiddleware-compress) [ContextTimeout](#webmiddleware-contexttimeout) [ContextTimeoutWithConfig](#webmiddleware-contexttimeoutwithconfig) [CreateExtractors](#webmiddleware-createextractors) [Decompress](#webmiddleware-decompress) [DecompressWithConfig](#webmiddleware-decompresswithconfig) [DefaultSkipper](#webmiddleware-defaultskipper) [ErrorBodyDump](#webmiddleware-errorbodydump) [ErrorBodyDumpWithConfig](#webmiddleware-errorbodydumpwithconfig) [Gzip](#webmiddleware-gzip) [GzipWithConfig](#webmiddleware-gzipwithconfig) [HTTPSNonWWWRedirect](#webmiddleware-httpsnonwwwredirect) [HTTPSNonWWWRedirectWithConfig](#webmiddleware-httpsnonwwwredirectwithconfig) [HTTPSRedirect](#webmiddleware-httpsredirect) [HTTPSRedirectWithConfig](#webmiddleware-httpsredirectwithconfig) [HTTPSWWWRedirect](#webmiddleware-httpswwwredirect) [HTTPSWWWRedirectWithConfig](#webmiddleware-httpswwwredirectwithconfig) [KeyAuth](#webmiddleware-keyauth) [KeyAuthWithConfig](#webmiddleware-keyauthwithconfig) [MethodFromForm](#webmiddleware-methodfromform) [MethodFromHeader](#webmiddleware-methodfromheader) [MethodFromQuery](#webmiddleware-methodfromquery) [MethodOverride](#webmiddleware-methodoverride) [MethodOverrideWithConfig](#webmiddleware-methodoverridewithconfig) [NewRandomBalancer](#webmiddleware-newrandombalancer) [NewRateLimiterMemoryStore](#webmiddleware-newratelimitermemorystore) [NewRateLimiterMemoryStoreWithConfig](#webmiddleware-newratelimitermemorystorewithconfig) [NewRoundRobinBalancer](#webmiddleware-newroundrobinbalancer) [NonWWWRedirect](#webmiddleware-nonwwwredirect) [NonWWWRedirectWithConfig](#webmiddleware-nonwwwredirectwithconfig) [Proxy](#webmiddleware-proxy) [ProxyWithConfig](#webmiddleware-proxywithconfig) [RateLimiter](#webmiddleware-ratelimiter) [RateLimiterMemoryStore.Allow](#webmiddleware-ratelimitermemorystore-allow) [RateLimiterWithConfig](#webmiddleware-ratelimiterwithconfig) [Recover](#webmiddleware-recover) [RecoverWithConfig](#webmiddleware-recoverwithconfig) [RemoveTrailingSlash](#webmiddleware-removetrailingslash) [RemoveTrailingSlashWithConfig](#webmiddleware-removetrailingslashwithconfig) [RequestID](#webmiddleware-requestid) [RequestIDWithConfig](#webmiddleware-requestidwithconfig) [RequestLoggerWithConfig](#webmiddleware-requestloggerwithconfig) [Rewrite](#webmiddleware-rewrite) [RewriteWithConfig](#webmiddleware-rewritewithconfig) [Secure](#webmiddleware-secure) [SecureWithConfig](#webmiddleware-securewithconfig) [Static](#webmiddleware-static) [StaticWithConfig](#webmiddleware-staticwithconfig) [Timeout](#webmiddleware-timeout) [TimeoutWithConfig](#webmiddleware-timeoutwithconfig) [WWWRedirect](#webmiddleware-wwwredirect) [WWWRedirectWithConfig](#webmiddleware-wwwredirectwithconfig) |
| **Prometheus** | [Default](#webprometheus-default) [Handler](#webprometheus-handler) [Metrics.Handler](#webprometheus-metrics-handler) [Metrics.Middleware](#webprometheus-metrics-middleware) [Middleware](#webprometheus-middleware) [MustNew](#webprometheus-mustnew) [New](#webprometheus-new) [RunPushGatewayGatherer](#webprometheus-runpushgatewaygatherer) [WriteGatheredMetrics](#webprometheus-writegatheredmetrics) |
| **Route Reporting** | [BuildRouteEntries](#buildrouteentries) [RenderRouteTable](#renderroutetable) |
| **Routing** | [MountRouter](#mountrouter) [NewRoute](#newroute) [NewRouteGroup](#newroutegroup) [NewWebSocketRoute](#newwebsocketroute) [RegisterRoutes](#registerroutes) [Route.Handler](#route-handler) [Route.HandlerName](#route-handlername) [Route.IsWebSocket](#route-iswebsocket) [Route.Method](#route-method) [Route.MiddlewareNames](#route-middlewarenames) [Route.Middlewares](#route-middlewares) [Route.Path](#route-path) [Route.WebSocketHandler](#route-websockethandler) [Route.WithMiddlewareNames](#route-withmiddlewarenames) [RouteGroup.MiddlewareNames](#routegroup-middlewarenames) [RouteGroup.Middlewares](#routegroup-middlewares) [RouteGroup.RoutePrefix](#routegroup-routeprefix) [RouteGroup.Routes](#routegroup-routes) [RouteGroup.WithMiddlewareNames](#routegroup-withmiddlewarenames) |
| **Testing** | [NewContext](#webtest-newcontext) |


## API Reference

_Generated from public API comments and examples._

### Adapter

#### <a id="echoweb-adapter-echo"></a>echoweb.Adapter.Echo

Echo returns the underlying Echo engine.

```go
adapter := echoweb.New()
fmt.Println(adapter.Echo() != nil)
// true
```

#### <a id="echoweb-adapter-router"></a>echoweb.Adapter.Router

Router returns the app-facing router contract.

```go
adapter := echoweb.New()
fmt.Println(adapter.Router() != nil)
// true
```

#### <a id="echoweb-adapter-servehttp"></a>echoweb.Adapter.ServeHTTP

ServeHTTP exposes the adapter as a standard http.Handler.

```go
adapter := echoweb.New()
adapter.Router().GET("/healthz", func(c web.Context) error { return c.NoContent(http.StatusOK) })
rr := httptest.NewRecorder()
req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
adapter.ServeHTTP(rr, req)
fmt.Println(rr.Code)
// 204
```

#### <a id="echoweb-new"></a>echoweb.New

New creates a new Echo-backed web adapter.

```go
adapter := echoweb.New()
fmt.Println(adapter.Router() != nil, adapter.Echo() != nil)
// true true
```

#### <a id="echoweb-newserver"></a>echoweb.NewServer

NewServer creates an Echo-backed server from web route groups and mounts.

```go
server, err := echoweb.NewServer(echoweb.ServerConfig{
	RouteGroups: []web.RouteGroup{
		web.NewRouteGroup("/api", []web.Route{
			web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return c.NoContent(http.StatusOK) }),
		}),
	},
})
fmt.Println(err == nil, server.Router() != nil)
// true true
```

#### <a id="echoweb-server-router"></a>echoweb.Server.Router

Router exposes the app-facing router contract.

```go
server, _ := echoweb.NewServer(echoweb.ServerConfig{})
fmt.Println(server.Router() != nil)
// true
```

#### <a id="echoweb-server-serve"></a>echoweb.Server.Serve

Serve starts the server and gracefully shuts it down when ctx is cancelled.

```go
server, _ := echoweb.NewServer(echoweb.ServerConfig{Addr: "127.0.0.1:0"})
ctx, cancel := context.WithCancel(context.Background())
cancel()
fmt.Println(server.Serve(ctx) == nil)
// true
```

#### <a id="echoweb-server-servehttp"></a>echoweb.Server.ServeHTTP

ServeHTTP exposes the server as an http.Handler for tests and local probing.

```go
server, _ := echoweb.NewServer(echoweb.ServerConfig{
	RouteGroups: []web.RouteGroup{
		web.NewRouteGroup("/api", []web.Route{
			web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return c.NoContent(http.StatusOK) }),
		}),
	},
})
rr := httptest.NewRecorder()
req := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
server.ServeHTTP(rr, req)
fmt.Println(rr.Code)
// 204
```

#### <a id="echoweb-unwrapcontext"></a>echoweb.UnwrapContext

UnwrapContext returns the underlying Echo context when the web.Context came from this adapter.

```go
adapter := echoweb.New()
adapter.Router().GET("/healthz", func(c web.Context) error {
	_, ok := echoweb.UnwrapContext(c)
	fmt.Println(ok)
	return c.NoContent(http.StatusOK)
})
rr := httptest.NewRecorder()
req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
adapter.ServeHTTP(rr, req)
// true
```

#### <a id="echoweb-unwrapwebsocketconn"></a>echoweb.UnwrapWebSocketConn

UnwrapWebSocketConn returns the underlying gorilla websocket connection.

```go
_, ok := echoweb.UnwrapWebSocketConn(nil)
fmt.Println(ok)
// false
```

#### <a id="echoweb-wrap"></a>echoweb.Wrap

Wrap exposes an existing Echo engine through the web.Router contract.

```go
adapter := echoweb.Wrap(nil)
fmt.Println(adapter.Echo() != nil)
// true
```

### Indexing

#### <a id="webindex-run"></a>webindex.Run

Run indexes API metadata from source and writes artifacts.

```go
manifest, err := webindex.Run(context.Background(), webindex.IndexOptions{
	Root:    ".",
	OutPath: "webindex.json",
})
fmt.Println(err == nil, manifest.Version != "")
// true true
```

### Middleware

#### <a id="webmiddleware-addtrailingslash"></a>webmiddleware.AddTrailingSlash

AddTrailingSlash adds a trailing slash to the request path.

```go
req := httptest.NewRequest(http.MethodGet, "/docs", nil)
ctx := webtest.NewContext(req, nil, "/docs", nil)
handler := webmiddleware.AddTrailingSlash()(func(c web.Context) error {
	fmt.Println(c.Request().URL.Path)
	return nil
})
_ = handler(ctx)
// /docs/
```

#### <a id="webmiddleware-addtrailingslashwithconfig"></a>webmiddleware.AddTrailingSlashWithConfig

AddTrailingSlashWithConfig returns trailing-slash middleware with config.

```go
req := httptest.NewRequest(http.MethodGet, "/docs", nil)
ctx := webtest.NewContext(req, nil, "/docs", nil)
handler := webmiddleware.AddTrailingSlashWithConfig(webmiddleware.TrailingSlashConfig{RedirectCode: 308})(func(c web.Context) error {
	return c.NoContent(204)
})
_ = handler(ctx)
fmt.Println(ctx.StatusCode(), ctx.Response().Header().Get("Location"))
// 308 /docs/
```

#### <a id="webmiddleware-basicauth"></a>webmiddleware.BasicAuth

BasicAuth returns basic auth middleware.

```go
mw := webmiddleware.BasicAuth(func(user, pass string, c web.Context) (bool, error) {
	return user == "demo" && pass == "secret", nil
})
req := httptest.NewRequest(http.MethodGet, "/", nil)
req.Header.Set("Authorization", "basic ZGVtbzpzZWNyZXQ=")
ctx := webtest.NewContext(req, nil, "/", nil)
handler := mw(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
_ = handler(ctx)
fmt.Println(ctx.StatusCode())
// 204
```

#### <a id="webmiddleware-basicauthwithconfig"></a>webmiddleware.BasicAuthWithConfig

BasicAuthWithConfig returns basic auth middleware with config.

```go
mw := webmiddleware.BasicAuthWithConfig(webmiddleware.BasicAuthConfig{
	Realm: "Example",
	Validator: func(user, pass string, c web.Context) (bool, error) { return true, nil },
})
ctx := webtest.NewContext(nil, nil, "/", nil)
handler := mw(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
_ = handler(ctx)
fmt.Println(ctx.StatusCode(), ctx.Response().Header().Get("WWW-Authenticate"))
// 401 basic realm=\"Example\"
```

#### <a id="webmiddleware-bodydump"></a>webmiddleware.BodyDump

BodyDump captures request and response payloads.

```go
var captured string
mw := webmiddleware.BodyDump(func(c web.Context, reqBody, resBody []byte) {
	captured = fmt.Sprintf("%s -> %s", string(reqBody), string(resBody))
})
req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("ping"))
ctx := webtest.NewContext(req, nil, "/", nil)
handler := mw(func(c web.Context) error { return c.Text(http.StatusOK, "pong") })
_ = handler(ctx)
fmt.Println(captured)
// ping -> pong
```

#### <a id="webmiddleware-bodydumpwithconfig"></a>webmiddleware.BodyDumpWithConfig

BodyDumpWithConfig captures request and response payloads with config.

```go
mw := webmiddleware.BodyDumpWithConfig(webmiddleware.BodyDumpConfig{
	Handler: func(c web.Context, reqBody, resBody []byte) { fmt.Println(string(resBody)) },
})
ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), nil, "/", nil)
handler := mw(func(c web.Context) error { return c.Text(http.StatusOK, "ok") })
_ = handler(ctx)
// ok
```

#### <a id="webmiddleware-bodylimit"></a>webmiddleware.BodyLimit

BodyLimit returns middleware that limits request body size.

```go
req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello"))
ctx := webtest.NewContext(req, nil, "/", nil)
handler := webmiddleware.BodyLimit("2B")(func(c web.Context) error {
	return c.NoContent(http.StatusOK)
})
_ = handler(ctx)
fmt.Println(ctx.StatusCode())
// 413
```

#### <a id="webmiddleware-bodylimitwithconfig"></a>webmiddleware.BodyLimitWithConfig

BodyLimitWithConfig returns body limit middleware with config.

```go
req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("ok"))
ctx := webtest.NewContext(req, nil, "/", nil)
handler := webmiddleware.BodyLimitWithConfig(webmiddleware.BodyLimitConfig{Limit: "2KB"})(func(c web.Context) error {
	return c.NoContent(http.StatusNoContent)
})
_ = handler(ctx)
fmt.Println(ctx.StatusCode())
// 204
```

#### <a id="webmiddleware-cors"></a>webmiddleware.CORS

CORS returns Cross-Origin Resource Sharing middleware.

```go
req := httptest.NewRequest(http.MethodGet, "/", nil)
req.Header.Set("Origin", "https://example.com")
ctx := webtest.NewContext(req, nil, "/", nil)
handler := webmiddleware.CORS()(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
_ = handler(ctx)
fmt.Println(ctx.Response().Header().Get("Access-Control-Allow-Origin"))
// *
```

#### <a id="webmiddleware-corswithconfig"></a>webmiddleware.CORSWithConfig

CORSWithConfig returns CORS middleware with config.

```go
mw := webmiddleware.CORSWithConfig(webmiddleware.CORSConfig{AllowOrigins: []string{"https://example.com"}})
req := httptest.NewRequest(http.MethodGet, "/", nil)
req.Header.Set("Origin", "https://example.com")
ctx := webtest.NewContext(req, nil, "/", nil)
handler := mw(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
_ = handler(ctx)
fmt.Println(ctx.Response().Header().Get("Access-Control-Allow-Origin"))
// https://example.com
```

#### <a id="webmiddleware-csrf"></a>webmiddleware.CSRF

CSRF enables token-based CSRF protection.

```go
ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), nil, "/", nil)
handler := webmiddleware.CSRF()(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
_ = handler(ctx)
fmt.Println(ctx.Response().Header().Get("Set-Cookie") != "")
// true
```

#### <a id="webmiddleware-csrfwithconfig"></a>webmiddleware.CSRFWithConfig

CSRFWithConfig enables token-based CSRF protection with config.

```go
mw := webmiddleware.CSRFWithConfig(webmiddleware.CSRFConfig{CookieName: "_csrf"})
ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), nil, "/", nil)
handler := mw(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
_ = handler(ctx)
fmt.Println(strings.Contains(ctx.Response().Header().Get("Set-Cookie"), "_csrf="))
// true
```

#### <a id="webmiddleware-compress"></a>webmiddleware.Compress

Compress is an alias for Gzip to match the checklist naming.

```go
req := httptest.NewRequest(http.MethodGet, "/", nil)
req.Header.Set("Accept-Encoding", "gzip")
ctx := webtest.NewContext(req, nil, "/", nil)
handler := webmiddleware.Compress()(func(c web.Context) error {
	return c.Text(http.StatusOK, "hello")
})
_ = handler(ctx)
fmt.Println(ctx.Response().Header().Get("Content-Encoding"))
// gzip
```

#### <a id="webmiddleware-contexttimeout"></a>webmiddleware.ContextTimeout

ContextTimeout sets a timeout on the request context.

```go
ctx := webtest.NewContext(nil, nil, "/", nil)
handler := webmiddleware.ContextTimeout(2 * time.Second)(func(c web.Context) error {
	fmt.Println(c.Request().Context().Err() == nil)
	return nil
})
_ = handler(ctx)
// true
```

#### <a id="webmiddleware-contexttimeoutwithconfig"></a>webmiddleware.ContextTimeoutWithConfig

ContextTimeoutWithConfig sets a timeout on the request context with config.

```go
ctx := webtest.NewContext(nil, nil, "/", nil)
handler := webmiddleware.ContextTimeoutWithConfig(webmiddleware.ContextTimeoutConfig{Timeout: time.Second})(func(c web.Context) error {
	fmt.Println(c.Request().Context().Err() == nil)
	return nil
})
_ = handler(ctx)
// true
```

#### <a id="webmiddleware-createextractors"></a>webmiddleware.CreateExtractors

CreateExtractors creates extractors from a lookup definition.

```go
extractors, err := webmiddleware.CreateExtractors("header:X-API-Key,query:token")
fmt.Println(err == nil, len(extractors))
// true 2
```

#### <a id="webmiddleware-decompress"></a>webmiddleware.Decompress

Decompress decompresses gzip-encoded request bodies.

```go
var body string
compressed := &bytes.Buffer{}
gz := gzip.NewWriter(compressed)
_, _ = gz.Write([]byte("hello"))
_ = gz.Close()
req := httptest.NewRequest(http.MethodPost, "/", compressed)
req.Header.Set("Content-Encoding", webmiddleware.GZIPEncoding)
ctx := webtest.NewContext(req, nil, "/", nil)
handler := webmiddleware.Decompress()(func(c web.Context) error {
	data, _ := io.ReadAll(c.Request().Body)
	body = string(data)
	return c.NoContent(http.StatusNoContent)
})
_ = handler(ctx)
fmt.Println(body, ctx.Request().Header.Get("Content-Encoding"))
// hello
```

#### <a id="webmiddleware-decompresswithconfig"></a>webmiddleware.DecompressWithConfig

DecompressWithConfig decompresses gzip-encoded request bodies with config.

```go
req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("plain"))
ctx := webtest.NewContext(req, nil, "/", nil)
handler := webmiddleware.DecompressWithConfig(webmiddleware.DecompressConfig{})(func(c web.Context) error {
	data, _ := io.ReadAll(c.Request().Body)
	fmt.Println(string(data))
	return nil
})
_ = handler(ctx)
// plain
```

#### <a id="webmiddleware-defaultskipper"></a>webmiddleware.DefaultSkipper

DefaultSkipper always runs the middleware.

```go
fmt.Println(webmiddleware.DefaultSkipper(nil))
// false
```

#### <a id="webmiddleware-errorbodydump"></a>webmiddleware.ErrorBodyDump

ErrorBodyDump captures response bodies for non-2xx and non-3xx responses.

```go
var captured string
mw := webmiddleware.ErrorBodyDump(func(c web.Context, status int, body []byte) {
	captured = fmt.Sprintf("%d:%s", status, string(body))
})
ctx := webtest.NewContext(nil, nil, "/", nil)
handler := mw(func(c web.Context) error { return c.Text(http.StatusBadRequest, "nope") })
_ = handler(ctx)
fmt.Println(captured)
// 400:nope
```

#### <a id="webmiddleware-errorbodydumpwithconfig"></a>webmiddleware.ErrorBodyDumpWithConfig

ErrorBodyDumpWithConfig captures response bodies for non-success responses with config.

```go
mw := webmiddleware.ErrorBodyDumpWithConfig(webmiddleware.ErrorBodyDumpConfig{
	Handler: func(c web.Context, status int, body []byte) { fmt.Println(status) },
})
ctx := webtest.NewContext(nil, nil, "/", nil)
handler := mw(func(c web.Context) error { return c.Text(http.StatusInternalServerError, "boom") })
_ = handler(ctx)
// 500
```

#### <a id="webmiddleware-gzip"></a>webmiddleware.Gzip

Gzip compresses responses with gzip.

```go
req := httptest.NewRequest(http.MethodGet, "/", nil)
req.Header.Set("Accept-Encoding", "gzip")
ctx := webtest.NewContext(req, nil, "/", nil)
handler := webmiddleware.Gzip()(func(c web.Context) error {
	return c.Text(http.StatusOK, "hello")
})
_ = handler(ctx)
fmt.Println(ctx.Response().Header().Get("Content-Encoding"))
// gzip
```

#### <a id="webmiddleware-gzipwithconfig"></a>webmiddleware.GzipWithConfig

GzipWithConfig compresses responses with gzip and config.

```go
req := httptest.NewRequest(http.MethodGet, "/", nil)
req.Header.Set("Accept-Encoding", "gzip")
ctx := webtest.NewContext(req, nil, "/", nil)
handler := webmiddleware.GzipWithConfig(webmiddleware.GzipConfig{MinLength: 256})(func(c web.Context) error {
	return c.Text(http.StatusOK, "short")
})
_ = handler(ctx)
fmt.Println(ctx.Response().Header().Get("Content-Encoding") == "")
// true
```

#### <a id="webmiddleware-httpsnonwwwredirect"></a>webmiddleware.HTTPSNonWWWRedirect

HTTPSNonWWWRedirect redirects to https without www.

```go
req := httptest.NewRequest(http.MethodGet, "http://www.example.com/docs", nil)
ctx := webtest.NewContext(req, nil, "/docs", nil)
_ = webmiddleware.HTTPSNonWWWRedirect()(func(c web.Context) error { return nil })(ctx)
fmt.Println(ctx.Response().Header().Get("Location"))
// https://example.com/docs
```

#### <a id="webmiddleware-httpsnonwwwredirectwithconfig"></a>webmiddleware.HTTPSNonWWWRedirectWithConfig

HTTPSNonWWWRedirectWithConfig returns HTTPS non-WWW redirect middleware with config.

```go
req := httptest.NewRequest(http.MethodGet, "http://www.example.com/docs", nil)
ctx := webtest.NewContext(req, nil, "/docs", nil)
_ = webmiddleware.HTTPSNonWWWRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})(func(c web.Context) error { return nil })(ctx)
fmt.Println(ctx.StatusCode())
// 307
```

#### <a id="webmiddleware-httpsredirect"></a>webmiddleware.HTTPSRedirect

HTTPSRedirect redirects http requests to https.

```go
req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
ctx := webtest.NewContext(req, nil, "/docs", nil)
_ = webmiddleware.HTTPSRedirect()(func(c web.Context) error { return nil })(ctx)
fmt.Println(ctx.StatusCode(), ctx.Response().Header().Get("Location"))
// 301 https://example.com/docs
```

#### <a id="webmiddleware-httpsredirectwithconfig"></a>webmiddleware.HTTPSRedirectWithConfig

HTTPSRedirectWithConfig returns HTTPS redirect middleware with config.

```go
req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
ctx := webtest.NewContext(req, nil, "/docs", nil)
_ = webmiddleware.HTTPSRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})(func(c web.Context) error { return nil })(ctx)
fmt.Println(ctx.StatusCode())
// 307
```

#### <a id="webmiddleware-httpswwwredirect"></a>webmiddleware.HTTPSWWWRedirect

HTTPSWWWRedirect redirects to https + www.

```go
req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
ctx := webtest.NewContext(req, nil, "/docs", nil)
_ = webmiddleware.HTTPSWWWRedirect()(func(c web.Context) error { return nil })(ctx)
fmt.Println(ctx.Response().Header().Get("Location"))
// https://www.example.com/docs
```

#### <a id="webmiddleware-httpswwwredirectwithconfig"></a>webmiddleware.HTTPSWWWRedirectWithConfig

HTTPSWWWRedirectWithConfig returns HTTPS+WWW redirect middleware with config.

```go
req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
ctx := webtest.NewContext(req, nil, "/docs", nil)
_ = webmiddleware.HTTPSWWWRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})(func(c web.Context) error { return nil })(ctx)
fmt.Println(ctx.StatusCode())
// 307
```

#### <a id="webmiddleware-keyauth"></a>webmiddleware.KeyAuth

KeyAuth returns key auth middleware.

```go
mw := webmiddleware.KeyAuth(func(key string, c web.Context) (bool, error) {
	return key == "demo-key", nil
})
req := httptest.NewRequest(http.MethodGet, "/", nil)
req.Header.Set("Authorization", "Bearer demo-key")
ctx := webtest.NewContext(req, nil, "/", nil)
handler := mw(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
_ = handler(ctx)
fmt.Println(ctx.StatusCode())
// 204
```

#### <a id="webmiddleware-keyauthwithconfig"></a>webmiddleware.KeyAuthWithConfig

KeyAuthWithConfig returns key auth middleware with config.

```go
mw := webmiddleware.KeyAuthWithConfig(webmiddleware.KeyAuthConfig{
	Validator: func(key string, c web.Context) (bool, error) { return true, nil },
})
ctx := webtest.NewContext(nil, nil, "/", nil)
handler := mw(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
_ = handler(ctx)
fmt.Println(ctx.StatusCode())
// 400
```

#### <a id="webmiddleware-methodfromform"></a>webmiddleware.MethodFromForm

MethodFromForm gets an override method from a form field.

```go
getter := webmiddleware.MethodFromForm("_method")
req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("_method=DELETE"))
req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
ctx := webtest.NewContext(req, nil, "/", nil)
fmt.Println(getter(ctx))
// DELETE
```

#### <a id="webmiddleware-methodfromheader"></a>webmiddleware.MethodFromHeader

MethodFromHeader gets an override method from a request header.

```go
getter := webmiddleware.MethodFromHeader("X-HTTP-Method-Override")
ctx := webtest.NewContext(nil, nil, "/", nil)
ctx.Request().Header.Set("X-HTTP-Method-Override", "PATCH")
fmt.Println(getter(ctx))
// PATCH
```

#### <a id="webmiddleware-methodfromquery"></a>webmiddleware.MethodFromQuery

MethodFromQuery gets an override method from a query parameter.

```go
getter := webmiddleware.MethodFromQuery("_method")
req := httptest.NewRequest(http.MethodPost, "/?_method=PUT", nil)
ctx := webtest.NewContext(req, nil, "/", nil)
fmt.Println(getter(ctx))
// PUT
```

#### <a id="webmiddleware-methodoverride"></a>webmiddleware.MethodOverride

MethodOverride returns method override middleware.

```go
req := httptest.NewRequest(http.MethodPost, "/", nil)
req.Header.Set("X-HTTP-Method-Override", http.MethodPatch)
ctx := webtest.NewContext(req, nil, "/", nil)
handler := webmiddleware.MethodOverride()(func(c web.Context) error {
	fmt.Println(c.Method())
	return nil
})
_ = handler(ctx)
// PATCH
```

#### <a id="webmiddleware-methodoverridewithconfig"></a>webmiddleware.MethodOverrideWithConfig

MethodOverrideWithConfig returns method override middleware with config.

```go
req := httptest.NewRequest(http.MethodPost, "/?_method=DELETE", nil)
ctx := webtest.NewContext(req, nil, "/", nil)
handler := webmiddleware.MethodOverrideWithConfig(webmiddleware.MethodOverrideConfig{
	Getter: webmiddleware.MethodFromQuery("_method"),
})(func(c web.Context) error {
	fmt.Println(c.Method())
	return nil
})
_ = handler(ctx)
// DELETE
```

#### <a id="webmiddleware-newrandombalancer"></a>webmiddleware.NewRandomBalancer

NewRandomBalancer creates a random proxy balancer.

```go
target, _ := url.Parse("http://localhost:8080")
balancer := webmiddleware.NewRandomBalancer([]*webmiddleware.ProxyTarget{{URL: target}})
fmt.Println(balancer.Next(nil).URL.Host)
// localhost:8080
```

#### <a id="webmiddleware-newratelimitermemorystore"></a>webmiddleware.NewRateLimiterMemoryStore

NewRateLimiterMemoryStore creates an in-memory rate limiter store.

```go
store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
allowed1, _ := store.Allow("192.0.2.1")
allowed2, _ := store.Allow("192.0.2.1")
fmt.Println(allowed1, allowed2)
// true false
```

#### <a id="webmiddleware-newratelimitermemorystorewithconfig"></a>webmiddleware.NewRateLimiterMemoryStoreWithConfig

NewRateLimiterMemoryStoreWithConfig creates an in-memory rate limiter store with config.

```go
store := webmiddleware.NewRateLimiterMemoryStoreWithConfig(webmiddleware.RateLimiterMemoryStoreConfig{Rate: rate.Every(time.Second)})
allowed, _ := store.Allow("192.0.2.1")
fmt.Println(allowed)
// true
```

#### <a id="webmiddleware-newroundrobinbalancer"></a>webmiddleware.NewRoundRobinBalancer

NewRoundRobinBalancer creates a round-robin proxy balancer.

```go
target, _ := url.Parse("http://localhost:8080")
balancer := webmiddleware.NewRoundRobinBalancer([]*webmiddleware.ProxyTarget{{URL: target}})
fmt.Println(balancer.Next(nil).URL.Host)
// localhost:8080
```

#### <a id="webmiddleware-nonwwwredirect"></a>webmiddleware.NonWWWRedirect

NonWWWRedirect redirects to the non-www host.

```go
req := httptest.NewRequest(http.MethodGet, "http://www.example.com/docs", nil)
ctx := webtest.NewContext(req, nil, "/docs", nil)
_ = webmiddleware.NonWWWRedirect()(func(c web.Context) error { return nil })(ctx)
fmt.Println(ctx.Response().Header().Get("Location"))
// http://example.com/docs
```

#### <a id="webmiddleware-nonwwwredirectwithconfig"></a>webmiddleware.NonWWWRedirectWithConfig

NonWWWRedirectWithConfig returns non-WWW redirect middleware with config.

```go
req := httptest.NewRequest(http.MethodGet, "http://www.example.com/docs", nil)
ctx := webtest.NewContext(req, nil, "/docs", nil)
_ = webmiddleware.NonWWWRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})(func(c web.Context) error { return nil })(ctx)
fmt.Println(ctx.StatusCode())
// 307
```

#### <a id="webmiddleware-proxy"></a>webmiddleware.Proxy

Proxy creates a proxy middleware.

```go
target, _ := url.Parse("http://localhost:8080")
balancer := webmiddleware.NewRandomBalancer([]*webmiddleware.ProxyTarget{{URL: target}})
req := httptest.NewRequest(http.MethodGet, "/", nil)
ctx := webtest.NewContext(req, nil, "/", nil)
_ = webmiddleware.Proxy(balancer)(func(c web.Context) error { return nil })(ctx)
fmt.Println(ctx.Get("target").(*webmiddleware.ProxyTarget).URL.Host)
// localhost:8080
```

#### <a id="webmiddleware-proxywithconfig"></a>webmiddleware.ProxyWithConfig

ProxyWithConfig creates a proxy middleware with config.

```go
target, _ := url.Parse("http://localhost:8080")
mw := webmiddleware.ProxyWithConfig(webmiddleware.ProxyConfig{
	Balancer: webmiddleware.NewRandomBalancer([]*webmiddleware.ProxyTarget{{URL: target}}),
})
req := httptest.NewRequest(http.MethodGet, "/old/path", nil)
ctx := webtest.NewContext(req, nil, "/", nil)
_ = mw(func(c web.Context) error { return nil })(ctx)
fmt.Println(ctx.Get("target").(*webmiddleware.ProxyTarget).URL.Host)
// localhost:8080
```

#### <a id="webmiddleware-ratelimiter"></a>webmiddleware.RateLimiter

RateLimiter creates a rate limiting middleware.

```go
store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
handler := webmiddleware.RateLimiter(store)(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
req1 := httptest.NewRequest(http.MethodGet, "/", nil)
req1.RemoteAddr = "192.0.2.10:1234"
ctx1 := webtest.NewContext(req1, nil, "/", nil)
_ = handler(ctx1)
req2 := httptest.NewRequest(http.MethodGet, "/", nil)
req2.RemoteAddr = "192.0.2.10:1234"
ctx2 := webtest.NewContext(req2, nil, "/", nil)
_ = handler(ctx2)
fmt.Println(ctx1.StatusCode(), ctx2.StatusCode())
// 204 429
```

#### <a id="webmiddleware-ratelimitermemorystore-allow"></a>webmiddleware.RateLimiterMemoryStore.Allow

Allow checks whether the given identifier is allowed through.

```go
store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
allowed, err := store.Allow("127.0.0.1")
fmt.Println(err == nil, allowed)
// true true
```

#### <a id="webmiddleware-ratelimiterwithconfig"></a>webmiddleware.RateLimiterWithConfig

RateLimiterWithConfig creates a rate limiting middleware with config.

```go
store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
mw := webmiddleware.RateLimiterWithConfig(webmiddleware.RateLimiterConfig{Store: store})
ctx := webtest.NewContext(nil, nil, "/", nil)
handler := mw(func(c web.Context) error { return c.NoContent(http.StatusAccepted) })
_ = handler(ctx)
fmt.Println(ctx.StatusCode())
// 202
```

#### <a id="webmiddleware-recover"></a>webmiddleware.Recover

Recover returns middleware that recovers panics from the handler chain.

```go
ctx := webtest.NewContext(nil, nil, "/", nil)
handler := webmiddleware.Recover()(func(c web.Context) error {
	panic("boom")
})
fmt.Println(handler(ctx) != nil)
// true
```

#### <a id="webmiddleware-recoverwithconfig"></a>webmiddleware.RecoverWithConfig

RecoverWithConfig returns recover middleware with config.

```go
ctx := webtest.NewContext(nil, nil, "/", nil)
handler := webmiddleware.RecoverWithConfig(webmiddleware.RecoverConfig{DisableErrorHandler: true})(func(c web.Context) error {
	panic("boom")
})
fmt.Println(handler(ctx) != nil)
// true
```

#### <a id="webmiddleware-removetrailingslash"></a>webmiddleware.RemoveTrailingSlash

RemoveTrailingSlash removes the trailing slash from the request path.

```go
req := httptest.NewRequest(http.MethodGet, "/docs/", nil)
ctx := webtest.NewContext(req, nil, "/docs/", nil)
handler := webmiddleware.RemoveTrailingSlash()(func(c web.Context) error {
	fmt.Println(c.Request().URL.Path)
	return nil
})
_ = handler(ctx)
// /docs
```

#### <a id="webmiddleware-removetrailingslashwithconfig"></a>webmiddleware.RemoveTrailingSlashWithConfig

RemoveTrailingSlashWithConfig returns remove-trailing-slash middleware with config.

```go
req := httptest.NewRequest(http.MethodGet, "/docs/", nil)
ctx := webtest.NewContext(req, nil, "/docs/", nil)
handler := webmiddleware.RemoveTrailingSlashWithConfig(webmiddleware.TrailingSlashConfig{RedirectCode: 308})(func(c web.Context) error {
	return c.NoContent(204)
})
_ = handler(ctx)
fmt.Println(ctx.StatusCode(), ctx.Response().Header().Get("Location"))
// 308 /docs
```

#### <a id="webmiddleware-requestid"></a>webmiddleware.RequestID

RequestID returns middleware that sets a request id header and context value.

```go
mw := webmiddleware.RequestID()
handler := mw(func(c web.Context) error {
	_ = c.Get("request_id")
	return c.NoContent(http.StatusOK)
})
ctx := webtest.NewContext(nil, nil, "/", nil)
_ = handler(ctx)
fmt.Println(ctx.Response().Header().Get("X-Request-ID") != "")
// true
// true
```

#### <a id="webmiddleware-requestidwithconfig"></a>webmiddleware.RequestIDWithConfig

RequestIDWithConfig returns RequestID middleware with config.

```go
mw := webmiddleware.RequestIDWithConfig(webmiddleware.RequestIDConfig{
	Generator: func() string { return "fixed-id" },
})
handler := mw(func(c web.Context) error { return c.NoContent(http.StatusOK) })
ctx := webtest.NewContext(nil, nil, "/", nil)
_ = handler(ctx)
fmt.Println(ctx.Response().Header().Get("X-Request-ID"))
// fixed-id
```

#### <a id="webmiddleware-requestloggerwithconfig"></a>webmiddleware.RequestLoggerWithConfig

RequestLoggerWithConfig returns request logger middleware with config.

```go
var loggedURI string
mw := webmiddleware.RequestLoggerWithConfig(webmiddleware.RequestLoggerConfig{
	LogValuesFunc: func(c web.Context, values webmiddleware.RequestLoggerValues) error {
		loggedURI = values.URI
		return nil
	},
})
req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
ctx := webtest.NewContext(req, nil, "/users/:id", webtest.PathParams{"id": "42"})
handler := mw(func(c web.Context) error { return c.NoContent(http.StatusAccepted) })
_ = handler(ctx)
fmt.Println(loggedURI, ctx.StatusCode())
// /users/42 202
```

#### <a id="webmiddleware-rewrite"></a>webmiddleware.Rewrite

Rewrite rewrites the request path using wildcard rules.

```go
req := httptest.NewRequest(http.MethodGet, "/old/users", nil)
ctx := webtest.NewContext(req, nil, "/old/*", nil)
handler := webmiddleware.Rewrite(map[string]string{"/old/*": "/new/$1"})(func(c web.Context) error {
	fmt.Println(c.Request().URL.Path)
	return nil
})
_ = handler(ctx)
// /new/users
```

#### <a id="webmiddleware-rewritewithconfig"></a>webmiddleware.RewriteWithConfig

RewriteWithConfig rewrites the request path using wildcard and regex rules.

```go
req := httptest.NewRequest(http.MethodGet, "/old/users", nil)
ctx := webtest.NewContext(req, nil, "/old/*", nil)
handler := webmiddleware.RewriteWithConfig(webmiddleware.RewriteConfig{
	Rules: map[string]string{"/old/*": "/v2/$1"},
})(func(c web.Context) error {
	fmt.Println(c.Request().URL.Path)
	return nil
})
_ = handler(ctx)
// /v2/users
```

#### <a id="webmiddleware-secure"></a>webmiddleware.Secure

Secure sets security-oriented response headers.

```go
ctx := webtest.NewContext(nil, nil, "/", nil)
handler := webmiddleware.Secure()(func(c web.Context) error { return c.NoContent(http.StatusOK) })
_ = handler(ctx)
fmt.Println(ctx.Response().Header().Get("X-Frame-Options"))
// SAMEORIGIN
```

#### <a id="webmiddleware-securewithconfig"></a>webmiddleware.SecureWithConfig

SecureWithConfig sets security-oriented response headers with config.

```go
ctx := webtest.NewContext(nil, nil, "/", nil)
handler := webmiddleware.SecureWithConfig(webmiddleware.SecureConfig{ReferrerPolicy: "same-origin"})(func(c web.Context) error {
	return c.NoContent(http.StatusOK)
})
_ = handler(ctx)
fmt.Println(ctx.Response().Header().Get("Referrer-Policy"))
// same-origin
```

#### <a id="webmiddleware-static"></a>webmiddleware.Static

Static serves static content from the provided root.

```go
dir, _ := os.MkdirTemp("", "web-static-*")
defer os.RemoveAll(dir)
_ = os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o644)
req := httptest.NewRequest(http.MethodGet, "/hello.txt", nil)
ctx := webtest.NewContext(req, nil, "/hello.txt", nil)
_ = webmiddleware.Static(dir)(func(c web.Context) error { return c.NoContent(http.StatusNotFound) })(ctx)
fmt.Println(strings.TrimSpace(ctx.ResponseWriter().(*httptest.ResponseRecorder).Body.String()))
// hello
```

#### <a id="webmiddleware-staticwithconfig"></a>webmiddleware.StaticWithConfig

StaticWithConfig serves static content using config.

```go
dir, _ := os.MkdirTemp("", "web-static-*")
defer os.RemoveAll(dir)
_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>home</h1>"), 0o644)
req := httptest.NewRequest(http.MethodGet, "/", nil)
ctx := webtest.NewContext(req, nil, "/", nil)
_ = webmiddleware.StaticWithConfig(webmiddleware.StaticConfig{Root: dir})(func(c web.Context) error { return c.NoContent(http.StatusNotFound) })(ctx)
fmt.Println(strings.TrimSpace(ctx.ResponseWriter().(*httptest.ResponseRecorder).Body.String()))
// <h1>home</h1>
```

#### <a id="webmiddleware-timeout"></a>webmiddleware.Timeout

Timeout returns a response-timeout middleware.

```go
ctx := webtest.NewContext(nil, nil, "/", nil)
handler := webmiddleware.Timeout()(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
_ = handler(ctx)
fmt.Println(ctx.StatusCode())
// 204
```

#### <a id="webmiddleware-timeoutwithconfig"></a>webmiddleware.TimeoutWithConfig

TimeoutWithConfig returns a response-timeout middleware with config.

```go
ctx := webtest.NewContext(nil, nil, "/", nil)
handler := webmiddleware.TimeoutWithConfig(webmiddleware.TimeoutConfig{Timeout: time.Second})(func(c web.Context) error {
	return c.NoContent(http.StatusAccepted)
})
_ = handler(ctx)
fmt.Println(ctx.StatusCode())
// 202
```

#### <a id="webmiddleware-wwwredirect"></a>webmiddleware.WWWRedirect

WWWRedirect redirects to the www host.

```go
req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
ctx := webtest.NewContext(req, nil, "/docs", nil)
_ = webmiddleware.WWWRedirect()(func(c web.Context) error { return nil })(ctx)
fmt.Println(ctx.Response().Header().Get("Location"))
// http://www.example.com/docs
```

#### <a id="webmiddleware-wwwredirectwithconfig"></a>webmiddleware.WWWRedirectWithConfig

WWWRedirectWithConfig returns WWW redirect middleware with config.

```go
req := httptest.NewRequest(http.MethodGet, "http://example.com/docs", nil)
ctx := webtest.NewContext(req, nil, "/docs", nil)
_ = webmiddleware.WWWRedirectWithConfig(webmiddleware.RedirectConfig{Code: http.StatusTemporaryRedirect})(func(c web.Context) error { return nil })(ctx)
fmt.Println(ctx.StatusCode())
// 307
```

### Prometheus

#### <a id="webprometheus-default"></a>webprometheus.Default

Default returns the package-level Prometheus metrics instance.

```go
fmt.Println(webprometheus.Default() == webprometheus.Default())
// true
```

#### <a id="webprometheus-handler"></a>webprometheus.Handler

Handler returns the package-level Prometheus scrape handler.

```go
registry := prometheus.NewRegistry()
counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "demo_total", Help: "demo counter"})
registry.MustRegister(counter)
counter.Inc()
metrics, _ := webprometheus.New(webprometheus.Config{Registerer: prometheus.NewRegistry(), Gatherer: registry})
recorder := httptest.NewRecorder()
ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/metrics", nil), recorder, "/metrics", nil)
_ = metrics.Handler()(ctx)
fmt.Println(strings.Contains(recorder.Body.String(), "demo_total"))
// true
```

#### <a id="webprometheus-metrics-handler"></a>webprometheus.Metrics.Handler

Handler exposes the configured Prometheus metrics as a web.Handler.

```go
registry := prometheus.NewRegistry()
counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "demo_total", Help: "demo counter"})
registry.MustRegister(counter)
counter.Inc()
metrics, _ := webprometheus.New(webprometheus.Config{Registerer: prometheus.NewRegistry(), Gatherer: registry})
recorder := httptest.NewRecorder()
ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/metrics", nil), recorder, "/metrics", nil)
_ = metrics.Handler()(ctx)
fmt.Println(strings.Contains(recorder.Body.String(), "demo_total"))
// true
```

#### <a id="webprometheus-metrics-middleware"></a>webprometheus.Metrics.Middleware

Middleware records Prometheus metrics for each request.

```go
registry := prometheus.NewRegistry()
metrics, _ := webprometheus.New(webprometheus.Config{Registerer: registry, Gatherer: registry, Namespace: "example"})
handler := metrics.Middleware()(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/healthz", nil), nil, "/healthz", nil)
_ = handler(ctx)
out := &bytes.Buffer{}
_ = webprometheus.WriteGatheredMetrics(out, registry)
fmt.Println(strings.Contains(out.String(), "example_requests_total"))
// true
```

#### <a id="webprometheus-middleware"></a>webprometheus.Middleware

Middleware returns the package-level Prometheus middleware.

```go
registry := prometheus.NewRegistry()
metrics, _ := webprometheus.New(webprometheus.Config{Registerer: registry, Gatherer: registry, Namespace: "example"})
handler := metrics.Middleware()(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/healthz", nil), nil, "/healthz", nil)
_ = handler(ctx)
out := &bytes.Buffer{}
_ = webprometheus.WriteGatheredMetrics(out, registry)
fmt.Println(strings.Contains(out.String(), "example_requests_total"))
// true
```

#### <a id="webprometheus-mustnew"></a>webprometheus.MustNew

MustNew creates a Metrics instance and panics on registration errors.

```go
metrics := webprometheus.MustNew(webprometheus.Config{Registerer: prometheus.NewRegistry(), Gatherer: prometheus.NewRegistry()})
fmt.Println(metrics != nil)
// true
```

#### <a id="webprometheus-new"></a>webprometheus.New

New creates a Metrics instance backed by Prometheus collectors.

```go
metrics, err := webprometheus.New(webprometheus.Config{Namespace: "app"})
_ = metrics
fmt.Println(err == nil)
// true
```

#### <a id="webprometheus-runpushgatewaygatherer"></a>webprometheus.RunPushGatewayGatherer

RunPushGatewayGatherer starts pushing collected metrics until the context finishes.

```go
err := webprometheus.RunPushGatewayGatherer(context.Background(), webprometheus.PushGatewayConfig{})
fmt.Println(err != nil)
// true
```

#### <a id="webprometheus-writegatheredmetrics"></a>webprometheus.WriteGatheredMetrics

WriteGatheredMetrics gathers collected metrics and writes them to the given writer.

```go
var buf bytes.Buffer
err := webprometheus.WriteGatheredMetrics(&buf, prometheus.NewRegistry())
fmt.Println(err == nil)
// true
```

### Route Reporting

#### <a id="buildrouteentries"></a>BuildRouteEntries

BuildRouteEntries builds a sorted slice of route entries from registered groups and extra entries.

```go
entries := web.BuildRouteEntries([]web.RouteGroup{
	web.NewRouteGroup("/api", []web.Route{
		web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }),
	}),
})
fmt.Println(entries[0].Path, entries[0].Methods[0])
// /api/healthz GET
```

#### <a id="renderroutetable"></a>RenderRouteTable

RenderRouteTable renders a route table using simple ASCII borders and ANSI colors.

```go
table := web.RenderRouteTable([]web.RouteEntry{{
	Path:    "/api/healthz",
	Handler: "monitoring.Healthz",
	Methods: []string{"GET"},
}})
fmt.Println(strings.Contains(table, "/api/healthz"))
// true
```

### Routing

#### <a id="mountrouter"></a>MountRouter

MountRouter applies mount-style router configuration in declaration order.

```go
adapter := echoweb.New()
err := web.MountRouter(adapter.Router(), []web.RouterMount{
	func(r web.Router) error {
		r.GET("/healthz", func(c web.Context) error { return nil })
		return nil
	},
})
fmt.Println(err == nil)
// true
```

#### <a id="newroute"></a>NewRoute

NewRoute creates a new route using the app-facing web handler contract directly.

```go
route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error {
	return c.NoContent(http.StatusOK)
})
fmt.Println(route.Method(), route.Path())
// GET /healthz
```

#### <a id="newroutegroup"></a>NewRouteGroup

NewRouteGroup wraps routes and their accompanied web middleware.

```go
group := web.NewRouteGroup("/api", []web.Route{
	web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }),
})
fmt.Println(group.RoutePrefix(), len(group.Routes()))
// /api 1
```

#### <a id="newwebsocketroute"></a>NewWebSocketRoute

NewWebSocketRoute creates a websocket route using the app-facing websocket handler contract.

```go
route := web.NewWebSocketRoute("/ws", func(c web.Context, conn web.WebSocketConn) error {
	return nil
})
fmt.Println(route.IsWebSocket())
// true
```

#### <a id="registerroutes"></a>RegisterRoutes

RegisterRoutes registers route groups onto a router.

```go
adapter := echoweb.New()
groups := []web.RouteGroup{
	web.NewRouteGroup("/api", []web.Route{
		web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }),
	}),
}
err := web.RegisterRoutes(adapter.Router(), groups)
fmt.Println(err == nil)
// true
```

#### <a id="route-handler"></a>Route.Handler

Handler returns the route handler.

```go
route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error {
	return c.NoContent(http.StatusCreated)
})
ctx := webtest.NewContext(nil, nil, "/healthz", nil)
_ = route.Handler()(ctx)
fmt.Println(ctx.StatusCode())
// 201
```

#### <a id="route-handlername"></a>Route.HandlerName

HandlerName returns the original handler name for route reporting.

```go
route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil })
fmt.Println(route.HandlerName() != "")
// true
```

#### <a id="route-iswebsocket"></a>Route.IsWebSocket

IsWebSocket reports whether this route upgrades to a websocket connection.

```go
route := web.NewWebSocketRoute("/ws", func(c web.Context, conn web.WebSocketConn) error { return nil })
fmt.Println(route.IsWebSocket())
// true
```

#### <a id="route-method"></a>Route.Method

Method returns the HTTP method.

```go
route := web.NewRoute(http.MethodPost, "/users", func(c web.Context) error { return nil })
fmt.Println(route.Method())
// POST
```

#### <a id="route-middlewarenames"></a>Route.MiddlewareNames

MiddlewareNames returns original middleware names for route reporting.

```go
route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }).WithMiddlewareNames("auth")
fmt.Println(route.MiddlewareNames()[0])
// auth
```

#### <a id="route-middlewares"></a>Route.Middlewares

Middlewares returns the route middleware slice.

```go
route := web.NewRoute(
	http.MethodGet,
	"/healthz",
	func(c web.Context) error { return nil },
	func(next web.Handler) web.Handler { return next },
)
fmt.Println(len(route.Middlewares()))
// 1
```

#### <a id="route-path"></a>Route.Path

Path returns the path of the route.

```go
route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil })
fmt.Println(route.Path())
// /healthz
```

#### <a id="route-websockethandler"></a>Route.WebSocketHandler

WebSocketHandler returns the websocket route handler.

```go
route := web.NewWebSocketRoute("/ws", func(c web.Context, conn web.WebSocketConn) error {
	c.Set("ready", true)
	return nil
})
ctx := webtest.NewContext(nil, nil, "/ws", nil)
err := route.WebSocketHandler()(ctx, nil)
fmt.Println(err == nil, ctx.Get("ready"))
// true true
```

#### <a id="route-withmiddlewarenames"></a>Route.WithMiddlewareNames

WithMiddlewareNames attaches reporting-only middleware names to the route.

```go
route := web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }).WithMiddlewareNames("auth", "trace")
fmt.Println(len(route.MiddlewareNames()))
// 2
```

#### <a id="routegroup-middlewarenames"></a>RouteGroup.MiddlewareNames

MiddlewareNames returns original middleware names for route reporting.

```go
group := web.NewRouteGroup("/api", nil).WithMiddlewareNames("auth")
fmt.Println(group.MiddlewareNames()[0])
// auth
```

#### <a id="routegroup-middlewares"></a>RouteGroup.Middlewares

Middlewares returns the middleware slice for the group.

```go
group := web.NewRouteGroup("/api", nil, func(next web.Handler) web.Handler { return next })
fmt.Println(len(group.Middlewares()))
// 1
```

#### <a id="routegroup-routeprefix"></a>RouteGroup.RoutePrefix

RoutePrefix returns the group prefix.

```go
group := web.NewRouteGroup("/api", nil)
fmt.Println(group.RoutePrefix())
// /api
```

#### <a id="routegroup-routes"></a>RouteGroup.Routes

Routes returns the routes in the group.

```go
group := web.NewRouteGroup("/api", []web.Route{
	web.NewRoute(http.MethodGet, "/healthz", func(c web.Context) error { return nil }),
})
fmt.Println(len(group.Routes()))
// 1
```

#### <a id="routegroup-withmiddlewarenames"></a>RouteGroup.WithMiddlewareNames

WithMiddlewareNames attaches reporting-only middleware names to the group.

```go
group := web.NewRouteGroup("/api", nil).WithMiddlewareNames("auth", "trace")
fmt.Println(len(group.MiddlewareNames()))
// 2
```

### Testing

#### <a id="webtest-newcontext"></a>webtest.NewContext

NewContext creates a new test context around the provided request/recorder pair.

```go
req := httptest.NewRequest(http.MethodGet, "/users/42?expand=roles", nil)
ctx := webtest.NewContext(req, nil, "/users/:id", webtest.PathParams{"id": "42"})
fmt.Println(ctx.Param("id"), ctx.Query("expand"))
// 42 roles
```
<!-- api:embed:end -->
