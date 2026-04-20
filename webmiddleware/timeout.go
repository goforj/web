package webmiddleware

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/goforj/web"
)

// TimeoutConfig configures response timeouts.
type TimeoutConfig struct {
	Skipper                   Skipper
	ErrorMessage              string
	OnTimeoutRouteErrorHandler func(error, web.Context)
	Timeout                   time.Duration
}

// DefaultTimeoutConfig is the default timeout config.
var DefaultTimeoutConfig = TimeoutConfig{
	Skipper:      DefaultSkipper,
	ErrorMessage: "",
}

// Timeout returns a response-timeout middleware.
// @group Middleware
// Example:
// ctx := webtest.NewContext(nil, nil, "/", nil)
// handler := webmiddleware.Timeout()(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
// _ = handler(ctx)
// fmt.Println(ctx.StatusCode())
//	// 204
func Timeout() web.Middleware {
	return TimeoutWithConfig(DefaultTimeoutConfig)
}

// TimeoutWithConfig returns a response-timeout middleware with config.
// @group Middleware
// Example:
// ctx := webtest.NewContext(nil, nil, "/", nil)
// handler := webmiddleware.TimeoutWithConfig(webmiddleware.TimeoutConfig{Timeout: time.Second})(func(c web.Context) error {
// 	return c.NoContent(http.StatusAccepted)
// })
// _ = handler(ctx)
// fmt.Println(ctx.StatusCode())
//	// 202
func TimeoutWithConfig(config TimeoutConfig) web.Middleware {
	if config.Skipper == nil {
		config.Skipper = DefaultTimeoutConfig.Skipper
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			if config.Skipper(r) || config.Timeout == 0 {
				return next(r)
			}

			errCh := make(chan error, 1)
			wrapper := timeoutHandler{
				writer:     &ignorableWriter{ResponseWriter: r.ResponseWriter()},
				ctx:        r,
				handler:    next,
				errCh:      errCh,
				errHandler: config.OnTimeoutRouteErrorHandler,
			}
			handler := http.TimeoutHandler(wrapper, config.Timeout, config.ErrorMessage)
			handler.ServeHTTP(wrapper.writer, r.Request())

			select {
			case err := <-errCh:
				return err
			default:
				return nil
			}
		}
	}
}

type timeoutHandler struct {
	writer     *ignorableWriter
	ctx        web.Context
	handler    web.Handler
	errHandler func(error, web.Context)
	errCh      chan error
}

func (t timeoutHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	t.ctx.SetRequest(req)
	originalWriter := t.ctx.ResponseWriter()
	t.ctx.SetResponseWriter(rw)

	defer func() {
		if err := recover(); err != nil {
			t.ctx.SetResponseWriter(originalWriter)
			panic(err)
		}
	}()

	err := t.handler(t.ctx)
	if ctxErr := req.Context().Err(); ctxErr == context.DeadlineExceeded {
		if err != nil && t.errHandler != nil {
			t.errHandler(err, t.ctx)
		}
		return
	}
	if err != nil {
		t.writer.Ignore(true)
		t.ctx.SetResponseWriter(originalWriter)
		t.errCh <- err
		return
	}
	t.ctx.SetResponseWriter(originalWriter)
}

type ignorableWriter struct {
	http.ResponseWriter
	lock         sync.Mutex
	ignoreWrites bool
}

func (w *ignorableWriter) Ignore(ignore bool) {
	w.lock.Lock()
	w.ignoreWrites = ignore
	w.lock.Unlock()
}

func (w *ignorableWriter) WriteHeader(code int) {
	w.lock.Lock()
	defer w.lock.Unlock()
	if w.ignoreWrites {
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *ignorableWriter) Write(body []byte) (int, error) {
	w.lock.Lock()
	defer w.lock.Unlock()
	if w.ignoreWrites {
		return len(body), nil
	}
	return w.ResponseWriter.Write(body)
}
