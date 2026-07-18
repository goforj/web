package webmiddleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goforj/web"
)

// TestTimeoutWithConfigTerminalOutcomes verifies each terminal outcome keeps sole ownership of the response.
func TestTimeoutWithConfigTerminalOutcomes(t *testing.T) {
	t.Run("nil request bypasses timeout handling", func(t *testing.T) {
		called := false
		err := TimeoutWithConfig(TimeoutConfig{Timeout: time.Second})(func(web.Context) error {
			called = true
			return nil
		})(requestIsHTTPOnlyContext{})
		if err != nil || !called {
			t.Fatalf("TimeoutWithConfig(nil request) = (called %v, err %v), want (true, nil)", called, err)
		}
	})

	t.Run("nil standard context falls back to background", func(t *testing.T) {
		ctx := &timeoutNilStandardContext{mutableContext: newMutableContext(httptest.NewRequest(http.MethodGet, "/", nil))}
		err := TimeoutWithConfig(TimeoutConfig{Timeout: time.Second})(func(c web.Context) error {
			return c.NoContent(http.StatusAccepted)
		})(ctx)
		if err != nil {
			t.Fatalf("TimeoutWithConfig(nil context) error = %v", err)
		}
		if recorder := ctx.ResponseWriter().(*httptest.ResponseRecorder); recorder.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusAccepted)
		}
	})

	t.Run("expired deadline uses default response", func(t *testing.T) {
		requestContext, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		ctx := newMutableContext(httptest.NewRequest(http.MethodGet, "/", nil).WithContext(requestContext))
		called := false
		err := TimeoutWithConfig(TimeoutConfig{Timeout: time.Second})(func(web.Context) error {
			called = true
			return nil
		})(ctx)
		if err != nil || called {
			t.Fatalf("TimeoutWithConfig(expired) = (called %v, err %v), want (false, nil)", called, err)
		}
		recorder := ctx.ResponseWriter().(*httptest.ResponseRecorder)
		if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "<h1>Timeout</h1>") {
			t.Fatalf("timeout response = (%d, %q)", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("canceled request emits no deadline body", func(t *testing.T) {
		requestContext, cancel := context.WithCancel(context.Background())
		cancel()
		ctx := newMutableContext(httptest.NewRequest(http.MethodGet, "/", nil).WithContext(requestContext))
		err := TimeoutWithConfig(TimeoutConfig{Timeout: time.Second, ErrorMessage: "deadline only"})(func(web.Context) error {
			return errors.New("handler ran for a canceled request")
		})(ctx)
		if err != nil {
			t.Fatalf("TimeoutWithConfig(canceled) error = %v", err)
		}
		recorder := ctx.ResponseWriter().(*httptest.ResponseRecorder)
		if recorder.Code != http.StatusServiceUnavailable || recorder.Body.Len() != 0 {
			t.Fatalf("canceled response = (%d, %q), want status-only 503", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("handler error commits headers but not body", func(t *testing.T) {
		ctx := newMutableContext(httptest.NewRequest(http.MethodGet, "/", nil))
		originalWriter := ctx.ResponseWriter()
		handlerErr := errors.New("handler failed")
		err := TimeoutWithConfig(TimeoutConfig{Timeout: time.Second})(func(c web.Context) error {
			c.SetHeader("X-Handler", "ran")
			if writeErr := c.Text(http.StatusCreated, "discarded"); writeErr != nil {
				return writeErr
			}
			return handlerErr
		})(ctx)
		if !errors.Is(err, handlerErr) {
			t.Fatalf("TimeoutWithConfig(error) error = %v, want %v", err, handlerErr)
		}
		if ctx.ResponseWriter() != originalWriter {
			t.Fatal("handler error did not restore the original writer")
		}
		recorder := originalWriter.(*httptest.ResponseRecorder)
		if recorder.Header().Get("X-Handler") != "ran" || recorder.Body.Len() != 0 {
			t.Fatalf("error response = (header %q, body %q)", recorder.Header().Get("X-Handler"), recorder.Body.String())
		}
	})

	t.Run("handler panic restores ownership and repanics", func(t *testing.T) {
		ctx := newMutableContext(httptest.NewRequest(http.MethodGet, "/", nil))
		originalWriter := ctx.ResponseWriter()
		var candidateWriter http.ResponseWriter
		panicValue := func() (recovered any) {
			defer func() {
				recovered = recover()
			}()
			_ = TimeoutWithConfig(TimeoutConfig{Timeout: time.Second})(func(c web.Context) error {
				candidateWriter = c.ResponseWriter()
				panic("handler panic")
			})(ctx)
			return nil
		}()
		if panicValue != "handler panic" {
			t.Fatalf("panic = %#v, want %q", panicValue, "handler panic")
		}
		if ctx.ResponseWriter() != originalWriter {
			t.Fatal("handler panic did not restore the original writer")
		}
		if _, err := candidateWriter.Write([]byte("late")); !errors.Is(err, errTimeoutResponseComplete) {
			t.Fatalf("late write error = %v, want %v", err, errTimeoutResponseComplete)
		}
	})

	t.Run("empty success commits implicit OK", func(t *testing.T) {
		ctx := newMutableContext(httptest.NewRequest(http.MethodGet, "/", nil))
		err := TimeoutWithConfig(TimeoutConfig{Timeout: time.Second})(func(web.Context) error {
			return nil
		})(ctx)
		if err != nil {
			t.Fatalf("TimeoutWithConfig(empty success) error = %v", err)
		}
		if recorder := ctx.ResponseWriter().(*httptest.ResponseRecorder); recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
			t.Fatalf("empty success response = (%d, %q)", recorder.Code, recorder.Body.String())
		}
	})
}

// TestDetachTimeoutContextAdapterContracts verifies fallback, reuse, commit, and release contracts.
func TestDetachTimeoutContextAdapterContracts(t *testing.T) {
	t.Run("non-detaching context disables reuse when supported", func(t *testing.T) {
		ctx := &timeoutDisableReuseContext{mutableContext: newMutableContext(httptest.NewRequest(http.MethodGet, "/", nil))}
		execution, commit, release, detached := detachTimeoutContext(ctx)
		release()
		if execution != ctx || commit != nil || detached || !ctx.disabled {
			t.Fatalf("detachTimeoutContext() = (%T, commit %v, detached %v, disabled %v)", execution, commit != nil, detached, ctx.disabled)
		}
	})

	t.Run("nil detached context disables reuse", func(t *testing.T) {
		ctx := &timeoutNilDetachingContext{timeoutDisableReuseContext: &timeoutDisableReuseContext{
			mutableContext: newMutableContext(httptest.NewRequest(http.MethodGet, "/", nil)),
		}}
		execution, commit, release, detached := detachTimeoutContext(ctx)
		release()
		if execution != ctx || commit != nil || detached || !ctx.disabled {
			t.Fatalf("detachTimeoutContext(nil) = (%T, commit %v, detached %v, disabled %v)", execution, commit != nil, detached, ctx.disabled)
		}
	})

	t.Run("detached lifecycle hooks run after completion", func(t *testing.T) {
		original := newMutableContext(httptest.NewRequest(http.MethodGet, "/", nil))
		detached := &timeoutLifecycleContext{mutableContext: newMutableContext(original.Request().Clone(original.Context()))}
		ctx := &timeoutLifecycleDetacher{mutableContext: original, detached: detached}
		err := TimeoutWithConfig(TimeoutConfig{Timeout: time.Second})(func(c web.Context) error {
			if c != detached {
				return errors.New("handler did not receive the detached lifecycle context")
			}
			if detached.boundRequest == nil || detached.boundContext == nil || detached.boundRequest.Context() != detached.boundContext {
				return errors.New("timeout request was not bound through the adapter contract")
			}
			c.SetHeader("X-Detached", "yes")
			return c.Text(http.StatusCreated, "done")
		})(ctx)
		if err != nil {
			t.Fatalf("TimeoutWithConfig(detached) error = %v", err)
		}
		if !ctx.committed || !detached.restored || !detached.released {
			t.Fatalf("lifecycle = (committed %v, restored %v, released %v)", ctx.committed, detached.restored, detached.released)
		}
		recorder := original.ResponseWriter().(*httptest.ResponseRecorder)
		if recorder.Code != http.StatusCreated || recorder.Header().Get("X-Detached") != "yes" || recorder.Body.String() != "done" {
			t.Fatalf("detached response = (%d, %q, %q)", recorder.Code, recorder.Header().Get("X-Detached"), recorder.Body.String())
		}
	})
}

// TestTimeoutRunnerContainsLateErrorCallbackPanic verifies a losing handler cannot panic through its diagnostic callback.
func TestTimeoutRunnerContainsLateErrorCallbackPanic(t *testing.T) {
	requestContext, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	ctx := newMutableContext(httptest.NewRequest(http.MethodGet, "/", nil).WithContext(requestContext))
	handlerErr := errors.New("late handler error")
	callbackCalled := false
	resultCh := make(chan timeoutResult, 1)
	runTimeoutHandler(timeoutRun{
		ctx: ctx,
		handler: func(web.Context) error {
			return handlerErr
		},
		errHandler: func(err error, got web.Context) {
			callbackCalled = errors.Is(err, handlerErr) && got == ctx
			panic("callback panic")
		},
		requestCtx: requestContext,
		arbiter:    &timeoutArbiter{},
		resultCh:   resultCh,
	})
	result := <-resultCh
	if result.completed || !errors.Is(result.err, handlerErr) || !callbackCalled {
		t.Fatalf("timeout result = %#v, callback called = %v", result, callbackCalled)
	}
}

// TestAwaitTimeoutResultWaitsForEstablishedCompletion verifies cancellation cannot displace an existing completion winner.
func TestAwaitTimeoutResultWaitsForEstablishedCompletion(t *testing.T) {
	arbiter := &timeoutArbiter{}
	if !arbiter.tryComplete(context.Background()) {
		t.Fatal("completion did not claim a pending arbiter")
	}
	waitContext, cancel := context.WithCancel(context.Background())
	cancel()
	resultCh := make(chan timeoutResult)
	want := timeoutResult{completed: true}
	go func() {
		time.Sleep(time.Millisecond)
		resultCh <- want
	}()
	got, completed := awaitTimeoutResult(waitContext, arbiter, resultCh)
	if !completed || got != want {
		t.Fatalf("awaitTimeoutResult() = (%#v, %v), want (%#v, true)", got, completed, want)
	}
}

// TestTimeoutArbiterRetainsFirstOwner verifies repeated and competing claims cannot change the recorded outcome.
func TestTimeoutArbiterRetainsFirstOwner(t *testing.T) {
	t.Run("completion winner", func(t *testing.T) {
		arbiter := &timeoutArbiter{}
		if !arbiter.tryComplete(context.Background()) || !arbiter.tryComplete(context.Background()) {
			t.Fatal("completion winner was not stable")
		}
		if arbiter.tryCancel(context.Canceled) {
			t.Fatal("cancellation displaced the completion winner")
		}
	})

	t.Run("cancellation winner", func(t *testing.T) {
		arbiter := &timeoutArbiter{}
		if !arbiter.tryCancel(context.Canceled) || !arbiter.tryCancel(context.DeadlineExceeded) {
			t.Fatal("cancellation winner was not stable")
		}
		if arbiter.tryComplete(context.Background()) {
			t.Fatal("completion displaced the cancellation winner")
		}
		if !errors.Is(arbiter.cancellationError(), context.Canceled) {
			t.Fatalf("cancellation error = %v, want %v", arbiter.cancellationError(), context.Canceled)
		}
	})
}

// TestTimeoutResponseWriterCommitContracts verifies implicit status, isolated headers, and terminal write errors.
func TestTimeoutResponseWriterCommitContracts(t *testing.T) {
	t.Run("body implies OK and headers are isolated", func(t *testing.T) {
		writer := newTimeoutResponseWriter()
		writer.Header()["X-Trace"] = []string{"original"}
		if _, err := writer.Write([]byte("body")); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		writer.WriteHeader(http.StatusCreated)
		destination := httptest.NewRecorder()
		writer.commitResponse(destination)
		writer.Header()["X-Trace"][0] = "mutated"
		if destination.Code != http.StatusOK || destination.Body.String() != "body" || destination.Header().Get("X-Trace") != "original" {
			t.Fatalf("committed response = (%d, %q, %q)", destination.Code, destination.Header().Get("X-Trace"), destination.Body.String())
		}
		if _, err := writer.Write([]byte("late")); !errors.Is(err, errTimeoutResponseComplete) {
			t.Fatalf("late write error = %v, want %v", err, errTimeoutResponseComplete)
		}
	})

	t.Run("header-only error discards candidate body", func(t *testing.T) {
		writer := newTimeoutResponseWriter()
		writer.Header().Set("X-Error", "yes")
		writer.WriteHeader(http.StatusCreated)
		if _, err := writer.Write([]byte("discarded")); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		destination := httptest.NewRecorder()
		writer.commitHeaders(destination)
		if destination.Header().Get("X-Error") != "yes" || destination.Body.Len() != 0 {
			t.Fatalf("committed error headers = (%q, %q)", destination.Header().Get("X-Error"), destination.Body.String())
		}
	})

	t.Run("first abort reason remains authoritative", func(t *testing.T) {
		writer := newTimeoutResponseWriter()
		writer.abort(context.Canceled)
		writer.abort(http.ErrHandlerTimeout)
		if _, err := writer.Write([]byte("late")); !errors.Is(err, context.Canceled) {
			t.Fatalf("late write error = %v, want %v", err, context.Canceled)
		}
		if err := timeoutWriteError(context.Canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("timeoutWriteError() = %v, want %v", err, context.Canceled)
		}
	})
}

// timeoutNilStandardContext exposes an adapter with no usable standard context.
type timeoutNilStandardContext struct {
	*mutableContext
}

// Context returns nil to exercise the middleware's context normalization contract.
func (c *timeoutNilStandardContext) Context() context.Context {
	return nil
}

// timeoutDisableReuseContext records fallback protection for contexts that cannot detach.
type timeoutDisableReuseContext struct {
	*mutableContext
	disabled bool
}

// DisableReuse records that the middleware protected a context retained by timed work.
func (c *timeoutDisableReuseContext) DisableReuse() {
	c.disabled = true
}

// timeoutNilDetachingContext advertises detachment but cannot allocate an isolated context.
type timeoutNilDetachingContext struct {
	*timeoutDisableReuseContext
}

// DetachedContext returns nil to force the documented reuse-protection fallback.
func (c *timeoutNilDetachingContext) DetachedContext() (web.Context, func()) {
	return nil, nil
}

// timeoutLifecycleDetacher exposes observable commit behavior around an isolated context.
type timeoutLifecycleDetacher struct {
	*mutableContext
	detached  *timeoutLifecycleContext
	committed bool
}

// DetachedContext returns the isolated context and records successful state publication.
func (c *timeoutLifecycleDetacher) DetachedContext() (web.Context, func()) {
	return c.detached, func() {
		c.committed = true
	}
}

// timeoutLifecycleContext records adapter-specific timeout binding, restoration, and release.
type timeoutLifecycleContext struct {
	*mutableContext
	boundRequest *http.Request
	boundContext context.Context
	restored     bool
	released     bool
}

// BindTimeoutRequest records the independently owned request and deadline context.
func (c *timeoutLifecycleContext) BindTimeoutRequest(request *http.Request, requestContext context.Context) {
	c.boundRequest = request
	c.boundContext = requestContext
	c.request = request
}

// RestoreTimeoutRequest records cleanup of the adapter's deadline override.
func (c *timeoutLifecycleContext) RestoreTimeoutRequest() {
	c.restored = true
}

// ReleaseDetachedContext records release only after the timed execution is no longer observable.
func (c *timeoutLifecycleContext) ReleaseDetachedContext() {
	c.released = true
}
