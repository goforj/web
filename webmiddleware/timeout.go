package webmiddleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/goforj/web"
)

// TimeoutConfig configures response timeouts.
type TimeoutConfig struct {
	Skipper                    Skipper
	ErrorMessage               string
	OnTimeoutRouteErrorHandler func(error, web.Context)
	Timeout                    time.Duration
}

// DefaultTimeoutConfig is the default timeout config.
var DefaultTimeoutConfig = TimeoutConfig{
	Skipper:      DefaultSkipper,
	ErrorMessage: "",
}

// Timeout returns a response-timeout middleware.
// @group Middleware - Request Lifecycle
// Example:
// router := echoweb.New().Router()
// router.Use(webmiddleware.Timeout())
//
//	router.GET("/healthz", func(c web.Context) error {
//		return c.NoContent(204)
//	})
func Timeout() web.Middleware {
	return TimeoutWithConfig(DefaultTimeoutConfig)
}

// TimeoutWithConfig returns a response-timeout middleware with config.
// Timed work may run on an isolated native adapter context; request state that
// must cross the timeout boundary should use web.Context.Set and web.Context.Get.
// @group Middleware - Request Lifecycle
// Example:
// router := echoweb.New().Router()
//
//	router.Use(webmiddleware.TimeoutWithConfig(webmiddleware.TimeoutConfig{
//		Timeout:      time.Second,
//		ErrorMessage: "request timed out",
//	}))
func TimeoutWithConfig(config TimeoutConfig) web.Middleware {
	if config.Skipper == nil {
		config.Skipper = DefaultTimeoutConfig.Skipper
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			if config.Skipper(r) || config.Timeout == 0 {
				return next(r)
			}

			executionContext, commit, release, detached := detachTimeoutContext(r)
			defer release()
			request := executionContext.Request()
			if request == nil {
				return next(r)
			}
			effectiveContext := r.Context()
			if effectiveContext == nil {
				effectiveContext = context.Background()
			}
			timeoutContext, cancel := context.WithTimeout(effectiveContext, config.Timeout)
			defer cancel()
			request = request.WithContext(timeoutContext)

			originalWriter := r.ResponseWriter()
			if cancellationErr := timeoutContext.Err(); cancellationErr != nil {
				writeTimeoutResponse(originalWriter, cancellationErr, config.ErrorMessage)
				return nil
			}
			writer := newTimeoutResponseWriter()
			bindTimeoutExecution(executionContext, request, timeoutContext, writer)
			arbiter := &timeoutArbiter{}
			resultCh := make(chan timeoutResult, 1)
			go runTimeoutHandler(timeoutRun{
				ctx:        executionContext,
				handler:    next,
				errHandler: config.OnTimeoutRouteErrorHandler,
				requestCtx: timeoutContext,
				arbiter:    arbiter,
				resultCh:   resultCh,
			})

			result, completed := awaitTimeoutResult(timeoutContext, arbiter, resultCh)
			if !completed {
				cancellationErr := arbiter.cancellationError()
				writer.abort(timeoutWriteError(cancellationErr))
				writeTimeoutResponse(originalWriter, cancellationErr, config.ErrorMessage)
				return nil
			}

			restoreTimeoutExecution(executionContext, originalWriter, detached)
			if result.panicValue != nil {
				writer.abort(errTimeoutResponseComplete)
				panic(result.panicValue)
			}
			if commit != nil {
				commit()
			}
			if result.err != nil {
				writer.commitHeaders(originalWriter)
				return result.err
			}
			writer.commitResponse(originalWriter)
			return nil
		}
	}
}

// timeoutRun carries one independently owned handler execution.
type timeoutRun struct {
	ctx        web.Context
	handler    web.Handler
	errHandler func(error, web.Context)
	requestCtx context.Context
	arbiter    *timeoutArbiter
	resultCh   chan timeoutResult
}

// timeoutResult carries a handler result and whether completion won request ownership.
type timeoutResult struct {
	err        error
	panicValue any
	completed  bool
}

// detachTimeoutContext isolates adapters whose native request contexts are pooled.
func detachTimeoutContext(ctx web.Context) (web.Context, func(), func(), bool) {
	release := func() {}
	detacher, ok := ctx.(interface {
		DetachedContext() (web.Context, func())
	})
	if !ok {
		if protectable, supported := ctx.(interface{ DisableReuse() }); supported {
			protectable.DisableReuse()
		}
		return ctx, nil, release, false
	}
	detached, commit := detacher.DetachedContext()
	if detached == nil {
		if protectable, supported := ctx.(interface{ DisableReuse() }); supported {
			protectable.DisableReuse()
		}
		return ctx, nil, release, false
	}
	if releaser, supported := detached.(interface{ ReleaseDetachedContext() }); supported {
		release = releaser.ReleaseDetachedContext
	}
	return detached, commit, release, detached != ctx
}

// bindTimeoutExecution installs deadline and response ownership on an isolated context.
func bindTimeoutExecution(ctx web.Context, request *http.Request, requestCtx context.Context, writer http.ResponseWriter) {
	if binder, supported := ctx.(interface {
		BindTimeoutRequest(*http.Request, context.Context)
	}); supported {
		binder.BindTimeoutRequest(request, requestCtx)
	} else {
		ctx.SetRequest(request)
		web.BindContext(ctx, requestCtx)
	}
	ctx.SetResponseWriter(writer)
}

// restoreTimeoutExecution removes adapter-owned deadline state after completion wins.
func restoreTimeoutExecution(ctx web.Context, originalWriter http.ResponseWriter, detached bool) {
	if restorer, supported := ctx.(interface{ RestoreTimeoutRequest() }); supported {
		restorer.RestoreTimeoutRequest()
	}
	if !detached {
		ctx.SetResponseWriter(originalWriter)
	}
}

// runTimeoutHandler reports a handler result after atomically competing with cancellation.
func runTimeoutHandler(run timeoutRun) {
	result := timeoutResult{}
	defer func() {
		if panicValue := recover(); panicValue != nil {
			result.panicValue = panicValue
		}
		result.completed = run.arbiter.tryComplete(run.requestCtx)
		if !result.completed && result.err != nil && errors.Is(run.arbiter.cancellationError(), context.DeadlineExceeded) {
			invokeTimeoutErrorHandler(run.errHandler, result.err, run.ctx)
		}
		run.resultCh <- result
	}()
	result.err = run.handler(run.ctx)
}

// invokeTimeoutErrorHandler contains callback panics after the timed request has already lost ownership.
func invokeTimeoutErrorHandler(handler func(error, web.Context), err error, ctx web.Context) {
	if handler == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	handler(err, ctx)
}

// awaitTimeoutResult returns only after the arbiter identifies the authoritative owner.
func awaitTimeoutResult(ctx context.Context, arbiter *timeoutArbiter, resultCh <-chan timeoutResult) (timeoutResult, bool) {
	select {
	case result := <-resultCh:
		return result, result.completed
	case <-ctx.Done():
		if arbiter.tryCancel(ctx.Err()) {
			return timeoutResult{}, false
		}
		result := <-resultCh
		return result, result.completed
	}
}

// timeoutOutcome identifies which side owns the original request after the race.
type timeoutOutcome uint8

const (
	timeoutPending timeoutOutcome = iota
	timeoutCompleted
	timeoutCanceled
)

// timeoutArbiter serializes completion and cancellation into one authoritative winner.
type timeoutArbiter struct {
	mu              sync.Mutex
	outcome         timeoutOutcome
	cancellationErr error
}

// tryComplete claims ownership for the handler only while its deadline remains live.
func (a *timeoutArbiter) tryComplete(ctx context.Context) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.outcome != timeoutPending {
		return a.outcome == timeoutCompleted
	}
	if err := ctx.Err(); err != nil {
		a.outcome = timeoutCanceled
		a.cancellationErr = err
		return false
	}
	a.outcome = timeoutCompleted
	return true
}

// tryCancel claims ownership for cancellation unless completion already won.
func (a *timeoutArbiter) tryCancel(err error) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.outcome != timeoutPending {
		return a.outcome == timeoutCanceled
	}
	a.outcome = timeoutCanceled
	a.cancellationErr = err
	return true
}

// cancellationError returns the reason recorded by the cancellation winner.
func (a *timeoutArbiter) cancellationError() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cancellationErr
}

// timeoutWriteError maps deadline expiry to the net/http late-write contract.
func timeoutWriteError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return http.ErrHandlerTimeout
	}
	return err
}

var errTimeoutResponseComplete = errors.New("webmiddleware: timeout response completed")

// timeoutResponseWriter buffers a candidate response until completion owns the request.
type timeoutResponseWriter struct {
	mu          sync.Mutex
	header      http.Header
	body        bytes.Buffer
	status      int
	wroteHeader bool
	err         error
}

var _ http.Pusher = (*timeoutResponseWriter)(nil)

// newTimeoutResponseWriter constructs an isolated response buffer.
func newTimeoutResponseWriter() *timeoutResponseWriter {
	return &timeoutResponseWriter{header: make(http.Header)}
}

// Header returns the handler-owned response headers.
func (w *timeoutResponseWriter) Header() http.Header {
	return w.header
}

// WriteHeader records a candidate status while the handler owns the buffer.
func (w *timeoutResponseWriter) WriteHeader(code int) {
	if code < 100 || code > 999 {
		panic(fmt.Sprintf("invalid WriteHeader code %v", code))
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil || w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
}

// Write records a candidate body or returns the cancellation winner's error.
func (w *timeoutResponseWriter) Write(body []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	if !w.wroteHeader {
		w.status = http.StatusOK
		w.wroteHeader = true
	}
	return w.body.Write(body)
}

// Push preserves net/http TimeoutHandler's optional interface when server push is unavailable.
func (w *timeoutResponseWriter) Push(_ string, _ *http.PushOptions) error {
	return http.ErrNotSupported
}

// abort freezes the buffer so late writes cannot reach a reused request.
func (w *timeoutResponseWriter) abort(err error) {
	w.mu.Lock()
	if w.err == nil {
		w.err = err
	}
	w.mu.Unlock()
}

// commitHeaders preserves pre-error header behavior while discarding a failed response body.
func (w *timeoutResponseWriter) commitHeaders(destination http.ResponseWriter) {
	w.mu.Lock()
	w.err = errTimeoutResponseComplete
	copyTimeoutHeaders(destination.Header(), w.header)
	w.mu.Unlock()
}

// commitResponse publishes the completed candidate response exactly once.
func (w *timeoutResponseWriter) commitResponse(destination http.ResponseWriter) {
	w.mu.Lock()
	w.err = errTimeoutResponseComplete
	copyTimeoutHeaders(destination.Header(), w.header)
	status := w.status
	if !w.wroteHeader {
		status = http.StatusOK
	}
	body := append([]byte(nil), w.body.Bytes()...)
	w.mu.Unlock()

	destination.WriteHeader(status)
	_, _ = destination.Write(body)
}

// copyTimeoutHeaders copies header slices so late detached mutations cannot affect the live response.
func copyTimeoutHeaders(destination http.Header, source http.Header) {
	for key, values := range source {
		destination[key] = append([]string(nil), values...)
	}
}

// writeTimeoutResponse publishes the cancellation winner's terminal response.
func writeTimeoutResponse(writer http.ResponseWriter, err error, message string) {
	writer.WriteHeader(http.StatusServiceUnavailable)
	if !errors.Is(err, context.DeadlineExceeded) {
		return
	}
	if message == "" {
		message = "<html><head><title>Timeout</title></head><body><h1>Timeout</h1></body></html>"
	}
	_, _ = writer.Write([]byte(message))
}
