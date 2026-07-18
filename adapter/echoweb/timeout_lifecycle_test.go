package echoweb_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goforj/web"
	"github.com/goforj/web/adapter/echoweb"
	"github.com/goforj/web/webmiddleware"
	echo "github.com/labstack/echo/v5"
)

// timeoutContextKey identifies values that must survive timeout context derivation.
type timeoutContextKey struct{}

// TestTimeoutContextPreservesRequestStateAndCommitsSuccessfulChanges verifies the synchronous lifecycle contract.
func TestTimeoutContextPreservesRequestStateAndCommitsSuccessfulChanges(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	outerObserved := false
	router.Use(
		func(next web.Handler) web.Handler {
			return func(ctx web.Context) error {
				ctx.Set("request-state", "before")
				web.BindContext(ctx, context.WithValue(ctx.Context(), timeoutContextKey{}, "bound"))
				if carrier, ok := ctx.(interface{ SetAppSourceName(string) }); ok {
					carrier.SetAppSourceName("http")
				}
				err := next(ctx)
				if got := ctx.Get("request-state"); got != "after" {
					t.Errorf("committed request state = %#v, want after", got)
				}
				if got := ctx.Get("handler-state"); got != "created" {
					t.Errorf("committed handler state = %#v, want created", got)
				}
				if err := ctx.Context().Err(); err != nil {
					t.Errorf("outer Context error = %v, want healthy context", err)
				}
				if _, ok := ctx.Context().Deadline(); ok {
					t.Error("outer Context retained the internal timeout deadline")
				}
				if err := ctx.Request().Context().Err(); err != nil {
					t.Errorf("outer Request context error = %v, want healthy context", err)
				}
				rawRequest := web.RawRequest(ctx)
				if rawRequest == nil || rawRequest.Context().Err() != nil {
					t.Errorf("outer RawRequest context = %v, want healthy context", rawRequest)
				} else if _, ok := rawRequest.Context().Deadline(); ok {
					t.Error("outer RawRequest retained the internal timeout deadline")
				}
				outerObserved = true
				return err
			}
		},
		webmiddleware.TimeoutWithConfig(webmiddleware.TimeoutConfig{Timeout: time.Second}),
	)

	router.GET("/work/:id", func(ctx web.Context) error {
		if got := ctx.Param("id"); got != "42" {
			t.Errorf("Param(id) = %q, want 42", got)
		}
		if got := ctx.Context().Value(timeoutContextKey{}); got != "bound" {
			t.Errorf("Context value = %#v, want bound", got)
		}
		if got := ctx.Request().Context().Value(timeoutContextKey{}); got != "bound" {
			t.Errorf("request context value = %#v, want bound", got)
		}
		if _, ok := ctx.Context().Deadline(); !ok {
			t.Error("Context does not expose the timeout deadline")
		}
		if _, ok := ctx.Request().Context().Deadline(); !ok {
			t.Error("request context does not expose the timeout deadline")
		}
		if ctx.Context().Done() == nil || ctx.Request().Context().Done() == nil {
			t.Error("timeout cancellation is not observable through both context views")
		}
		provider, ok := ctx.Context().(interface{ AppSourceName() string })
		if !ok {
			t.Errorf("source metadata context type = %T, want AppSourceName provider", ctx.Context())
		} else if got := provider.AppSourceName(); got != "http" {
			t.Errorf("source metadata = %q, want http", got)
		}
		ctx.Set("request-state", "after")
		ctx.Set("handler-state", "created")
		return ctx.Text(http.StatusOK, "ok")
	})

	recorder := httptest.NewRecorder()
	adapter.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/work/42", nil))
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "ok" {
		t.Fatalf("response = (%d, %q), want (200, ok)", recorder.Code, recorder.Body.String())
	}
	if !outerObserved {
		t.Fatal("outer middleware did not resume after the successful handler")
	}
}

// TestTimeoutContextCommitsChangesBeforeReturningHandlerErrors verifies error-path middleware compatibility.
func TestTimeoutContextCommitsChangesBeforeReturningHandlerErrors(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	wantErr := errors.New("route failed")
	outerObserved := false
	router.Use(
		func(next web.Handler) web.Handler {
			return func(ctx web.Context) error {
				err := next(ctx)
				if !errors.Is(err, wantErr) {
					t.Errorf("handler error = %v, want %v", err, wantErr)
				}
				if got := ctx.Get("error-state"); got != "committed" {
					t.Errorf("committed error state = %#v, want committed", got)
				}
				outerObserved = true
				return err
			}
		},
		webmiddleware.TimeoutWithConfig(webmiddleware.TimeoutConfig{Timeout: time.Second}),
	)
	router.GET("/error", func(ctx web.Context) error {
		ctx.Set("error-state", "committed")
		return wantErr
	})

	recorder := httptest.NewRecorder()
	adapter.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/error", nil))
	if !outerObserved {
		t.Fatal("outer middleware did not observe the handler error")
	}
}

// TestTimeoutContextBridgesNativeValuesThroughWebGet verifies safe interoperability without private Echo reflection.
func TestTimeoutContextBridgesNativeValuesThroughWebGet(t *testing.T) {
	engine := echo.New()
	nativeAfter := false
	engine.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx *echo.Context) error {
			ctx.Set("native-value", "before")
			ctx.Set("native-nil", "before")
			err := next(ctx)
			if got := ctx.Get("native-value"); got != "after" {
				t.Errorf("committed native-value = %#v, want after", got)
			}
			if got := ctx.Get("native-nil"); got != nil {
				t.Errorf("committed native-nil = %#v, want nil", got)
			}
			nativeAfter = true
			return err
		}
	})

	adapter := echoweb.Wrap(engine)
	adapter.Router().Use(webmiddleware.TimeoutWithConfig(webmiddleware.TimeoutConfig{Timeout: time.Second}))
	adapter.Router().GET("/native", func(ctx web.Context) error {
		native, ok := echoweb.UnwrapContext(ctx)
		if !ok {
			t.Error("timed context did not expose its isolated native context")
		} else if got := native.Get("native-value"); got != nil {
			t.Errorf("isolated native store inherited pre-detach value %#v", got)
		}
		if got := ctx.Get("native-value"); got != "before" {
			t.Errorf("bridged native-value = %#v, want before", got)
		}
		ctx.Set("native-value", "after")
		ctx.Set("native-nil", nil)
		if got := ctx.Get("native-nil"); got != nil {
			t.Errorf("explicit nil = %#v, want nil", got)
		}
		return ctx.NoContent(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	adapter.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/native", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if !nativeAfter {
		t.Fatal("outer native middleware did not resume")
	}
}

// TestTimeoutContextIsolatesLateHandlerFromReusedEchoContext verifies request and response ownership after a deadline.
func TestTimeoutContextIsolatesLateHandlerFromReusedEchoContext(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstFinished := make(chan struct{})
	lateWriteErr := make(chan error, 1)
	callbackContext := make(chan web.Context, 1)
	postTimeoutState := make(chan any, 1)

	router.Use(
		func(next web.Handler) web.Handler {
			return func(ctx web.Context) error {
				ctx.Set("request-id", ctx.Header("X-Request-ID"))
				err := next(ctx)
				if ctx.Header("X-Request-ID") == "first-request" {
					postTimeoutState <- ctx.Get("late-state")
				}
				return err
			}
		},
		webmiddleware.TimeoutWithConfig(webmiddleware.TimeoutConfig{
			Timeout:      40 * time.Millisecond,
			ErrorMessage: "timeout",
			OnTimeoutRouteErrorHandler: func(_ error, ctx web.Context) {
				callbackContext <- ctx
			},
		}),
	)

	router.GET("/work/:id", func(ctx web.Context) error {
		switch ctx.Param("id") {
		case "first":
			close(firstStarted)
			<-releaseFirst
			if got := ctx.Get("request-id"); got != "first-request" {
				t.Errorf("late request state = %#v, want first-request", got)
			}
			if got := ctx.Param("id"); got != "first" {
				t.Errorf("late Param(id) = %q, want first", got)
			}
			if got := ctx.Request().Header.Get("X-Request-ID"); got != "first-request" {
				t.Errorf("late request header = %q, want first-request", got)
			}
			if !errors.Is(ctx.Context().Err(), context.DeadlineExceeded) {
				t.Errorf("late Context error = %v, want deadline exceeded", ctx.Context().Err())
			}
			ctx.Set("late-state", "must-not-commit")
			ctx.Request().Header.Set("X-Late-Mutation", "first")
			ctx.Request().URL.Path = "/mutated-late"
			err := ctx.Text(http.StatusCreated, "late-first")
			lateWriteErr <- err
			close(firstFinished)
			return err
		case "second":
			close(releaseFirst)
			<-firstFinished
			if got := ctx.Get("request-id"); got != "second-request" {
				t.Errorf("second request state = %#v, want second-request", got)
			}
			if got := ctx.Header("X-Late-Mutation"); got != "" {
				t.Errorf("second request inherited late header mutation %q", got)
			}
			if got := ctx.URI(); got != "/work/second" {
				t.Errorf("second URI = %q, want /work/second", got)
			}
			return ctx.Text(http.StatusOK, "second")
		default:
			return ctx.NoContent(http.StatusNotFound)
		}
	})

	firstRequest := httptest.NewRequest(http.MethodGet, "/work/first", nil)
	firstRequest.Header.Set("X-Request-ID", "first-request")
	firstRecorder := httptest.NewRecorder()
	adapter.ServeHTTP(firstRecorder, firstRequest)
	<-firstStarted
	if firstRecorder.Code != http.StatusServiceUnavailable || strings.TrimSpace(firstRecorder.Body.String()) != "timeout" {
		t.Fatalf("first response = (%d, %q), want (503, timeout)", firstRecorder.Code, firstRecorder.Body.String())
	}
	if got := <-postTimeoutState; got != nil {
		t.Fatalf("post-timeout state = %#v, want nil", got)
	}

	secondRequest := httptest.NewRequest(http.MethodGet, "/work/second", nil)
	secondRequest.Header.Set("X-Request-ID", "second-request")
	secondRecorder := httptest.NewRecorder()
	adapter.ServeHTTP(secondRecorder, secondRequest)
	if secondRecorder.Code != http.StatusOK || strings.TrimSpace(secondRecorder.Body.String()) != "second" {
		t.Fatalf("second response = (%d, %q), want (200, second)", secondRecorder.Code, secondRecorder.Body.String())
	}
	if err := <-lateWriteErr; !errors.Is(err, http.ErrHandlerTimeout) {
		t.Fatalf("late write error = %v, want %v", err, http.ErrHandlerTimeout)
	}
	if got := firstRequest.Header.Get("X-Late-Mutation"); got != "" {
		t.Fatalf("original request inherited late header mutation %q", got)
	}
	if got := firstRequest.URL.Path; got != "/work/first" {
		t.Fatalf("original request path = %q, want /work/first", got)
	}
	callback := <-callbackContext
	if got := callback.Get("request-id"); got != "first-request" {
		t.Fatalf("timeout callback state = %#v, want first-request", got)
	}
}

// TestTimeoutContextPreservesRootStateForScopedTimeouts verifies state ownership when Timeout is registered below root middleware.
func TestTimeoutContextPreservesRootStateForScopedTimeouts(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		group bool
	}{
		{name: "route", key: "request-id"},
		{name: "route empty key", key: ""},
		{name: "group", key: "request-id", group: true},
		{name: "group empty key", key: "", group: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := echoweb.New()
			router := adapter.Router()
			started := make(chan struct{})
			release := make(chan struct{})
			observed := make(chan any, 1)
			router.Use(func(next web.Handler) web.Handler {
				return func(ctx web.Context) error {
					ctx.Set(test.key, "root-state")
					return next(ctx)
				}
			})

			timeout := webmiddleware.TimeoutWithConfig(webmiddleware.TimeoutConfig{
				Timeout:      20 * time.Millisecond,
				ErrorMessage: "timeout",
			})
			handler := func(ctx web.Context) error {
				close(started)
				<-release
				observed <- ctx.Get(test.key)
				return nil
			}
			path := "/scoped-timeout"
			if test.group {
				router.Group("/group", timeout).GET(path, handler)
				path = "/group" + path
			} else {
				router.GET(path, handler, timeout)
			}

			recorder := httptest.NewRecorder()
			adapter.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			<-started
			if recorder.Code != http.StatusServiceUnavailable || strings.TrimSpace(recorder.Body.String()) != "timeout" {
				t.Fatalf("response = (%d, %q), want (503, timeout)", recorder.Code, recorder.Body.String())
			}
			close(release)
			if got := <-observed; got != "root-state" {
				t.Fatalf("late scoped state = %#v, want root-state", got)
			}
		})
	}
}

// TestTimeoutContextPanicLeavesOriginalRequestHealthy verifies recovery and subsequent pooled reuse.
func TestTimeoutContextPanicLeavesOriginalRequestHealthy(t *testing.T) {
	adapter := echoweb.New()
	router := adapter.Router()
	recovered := false
	router.Use(
		func(next web.Handler) web.Handler {
			return func(ctx web.Context) (err error) {
				defer func() {
					if recover() == nil {
						return
					}
					recovered = true
					if got := ctx.Get("panic-state"); got != nil {
						t.Errorf("panic state committed as %#v", got)
					}
					if contextErr := ctx.Context().Err(); contextErr != nil {
						t.Errorf("recovery context error = %v", contextErr)
					}
					err = ctx.Text(http.StatusInternalServerError, "recovered")
				}()
				return next(ctx)
			}
		},
		webmiddleware.TimeoutWithConfig(webmiddleware.TimeoutConfig{Timeout: time.Second}),
	)
	router.GET("/panic", func(ctx web.Context) error {
		ctx.Set("panic-state", "must-not-commit")
		panic("boom")
	})
	router.GET("/healthy", func(ctx web.Context) error {
		if got := ctx.Get("panic-state"); got != nil {
			t.Errorf("subsequent request inherited panic state %#v", got)
		}
		return ctx.Text(http.StatusOK, "healthy")
	})

	panicRecorder := httptest.NewRecorder()
	adapter.ServeHTTP(panicRecorder, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if !recovered || panicRecorder.Code != http.StatusInternalServerError || strings.TrimSpace(panicRecorder.Body.String()) != "recovered" {
		t.Fatalf("panic response = recovered:%v status:%d body:%q", recovered, panicRecorder.Code, panicRecorder.Body.String())
	}

	healthyRecorder := httptest.NewRecorder()
	adapter.ServeHTTP(healthyRecorder, httptest.NewRequest(http.MethodGet, "/healthy", nil))
	if healthyRecorder.Code != http.StatusOK || strings.TrimSpace(healthyRecorder.Body.String()) != "healthy" {
		t.Fatalf("healthy response = (%d, %q)", healthyRecorder.Code, healthyRecorder.Body.String())
	}
}

// TestTimeoutContextAlreadyCanceledParentIsDeterministic verifies cancellation owns response and state.
func TestTimeoutContextAlreadyCanceledParentIsDeterministic(t *testing.T) {
	adapter := echoweb.New()
	handlerCalls := 0
	outerCalls := 0
	adapter.Router().Use(
		func(next web.Handler) web.Handler {
			return func(ctx web.Context) error {
				ctx.Set("outer-state", "before")
				err := next(ctx)
				if got := ctx.Get("handler-state"); got != nil {
					t.Errorf("canceled request committed handler state %#v", got)
				}
				outerCalls++
				return err
			}
		},
		webmiddleware.TimeoutWithConfig(webmiddleware.TimeoutConfig{
			Timeout:      time.Second,
			ErrorMessage: "deadline-only",
		}),
	)
	adapter.Router().GET("/canceled", func(ctx web.Context) error {
		handlerCalls++
		ctx.Set("handler-state", "must-not-commit")
		return errors.New("must-not-return")
	})

	for range 100 {
		request := httptest.NewRequest(http.MethodGet, "/canceled", nil)
		requestContext, cancel := context.WithCancel(request.Context())
		cancel()
		request = request.WithContext(requestContext)
		recorder := httptest.NewRecorder()
		adapter.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusServiceUnavailable || recorder.Body.Len() != 0 {
			t.Fatalf("canceled response = (%d, %q), want empty 503", recorder.Code, recorder.Body.String())
		}
	}
	if handlerCalls != 0 || outerCalls != 100 {
		t.Fatalf("calls = handler:%d outer:%d, want 0 and 100", handlerCalls, outerCalls)
	}
}
