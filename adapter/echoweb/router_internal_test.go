package echoweb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goforj/web"
	echo "github.com/labstack/echo/v5"
)

// TestRootMiddlewareContextPromotesInternalAndInlineState verifies internal metadata bypasses inline application state.
func TestRootMiddlewareContextPromotesInternalAndInlineState(t *testing.T) {
	var missing *rootMiddlewareContext
	if got := missing.echoContext(); got != nil {
		t.Fatalf("nil echoContext() = %v, want nil", got)
	}
	missing.promoteStateToNative()

	engine := echo.New()
	native := engine.NewContext(
		httptest.NewRequest(http.MethodGet, "/", nil),
		httptest.NewRecorder(),
	)
	ctx := &rootMiddlewareContext{contextAdapter: acquireContextAdapter(native)}
	ctx.Set("request-id", "req-1")
	if got, want := ctx.Get("request-id"), "req-1"; got != want {
		t.Fatalf("Get(request-id) = %#v, want %#v", got, want)
	}

	ctx.Set(contextAdapterStateKey, "internal")
	if got, want := native.Get(contextAdapterStateKey), "internal"; got != want {
		t.Fatalf("native internal state = %#v, want %#v", got, want)
	}
}

// TestRouterHandlesEmptyAndInvalidMiddlewareState verifies defensive paths fail safely without a request owner.
func TestRouterHandlesEmptyAndInvalidMiddlewareState(t *testing.T) {
	if err := (*webRouteHandler)(nil).handle(nil); !errors.Is(err, echo.ErrInternalServerError) {
		t.Fatalf("nil web route error = %v, want internal server error", err)
	}

	router := &routerAdapter{}
	router.Pre(func(next web.Handler) web.Handler { return next })
	router.Use(nil)
	router.appendRootMiddlewares(nil)
	if head, tail := router.compileRootMiddlewareSegment(nil); head != nil || tail != nil {
		t.Fatalf("empty middleware segment = (%v, %v), want (nil, nil)", head, tail)
	}
	if got := (*routerAdapter)(nil).middlewareSnapshot(); got != nil {
		t.Fatalf("nil middleware snapshot = %#v, want nil", got)
	}

	var missing *rootMiddlewareContext
	if err := router.invokeRootMiddlewareNext(missing); !errors.Is(err, echo.ErrInternalServerError) {
		t.Fatalf("typed nil root error = %v, want internal server error", err)
	}
	if err := router.invokeRootMiddlewareNext(nil); !errors.Is(err, echo.ErrInternalServerError) {
		t.Fatalf("unrecognized context error = %v, want internal server error", err)
	}
}

// TestAdaptHandlerAndInvalidNativeMiddlewareContext verifies both valid adapter entry and invalid middleware continuation.
func TestAdaptHandlerAndInvalidNativeMiddlewareContext(t *testing.T) {
	engine := echo.New()
	native := engine.NewContext(
		httptest.NewRequest(http.MethodGet, "/", nil),
		httptest.NewRecorder(),
	)
	called := false
	handler := adaptHandler(func(ctx web.Context) error {
		called = true
		if got := ctx.Native(); got != native {
			t.Fatalf("Native() = %v, want original Echo context", got)
		}
		return nil
	})
	if err := handler(native); err != nil {
		t.Fatalf("adapted handler error = %v", err)
	}
	if !called {
		t.Fatal("adapted handler was not called")
	}

	invalid := adaptMiddlewares([]web.Middleware{
		func(next web.Handler) web.Handler {
			return func(web.Context) error {
				return next(nil)
			}
		},
	})(func(*echo.Context) error { return nil })
	if err := invalid(native); !errors.Is(err, echo.ErrInternalServerError) {
		t.Fatalf("invalid middleware context error = %v, want internal server error", err)
	}
}
