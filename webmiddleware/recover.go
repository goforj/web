package webmiddleware

import (
	"fmt"
	"net/http"
	"runtime"

	"github.com/goforj/web"
)

// RecoverConfig configures recover middleware.
type RecoverConfig struct {
	StackSize           int
	DisableStack        bool
	DisableErrorHandler bool
	HandleError         func(web.Context, error, []byte) error
}

// DefaultRecoverConfig is the default Recover middleware config.
var DefaultRecoverConfig = RecoverConfig{
	StackSize: 4 << 10,
}

// Recover returns middleware that recovers panics from the handler chain.
// @group Middleware
// Example:
// _ = webmiddleware.Recover()
func Recover() web.Middleware {
	return RecoverWithConfig(DefaultRecoverConfig)
}

// RecoverWithConfig returns recover middleware with config.
// @group Middleware
// Example:
// _ = webmiddleware.RecoverWithConfig(webmiddleware.RecoverConfig{})
func RecoverWithConfig(config RecoverConfig) web.Middleware {
	if config.StackSize == 0 {
		config.StackSize = DefaultRecoverConfig.StackSize
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) (returnErr error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					if recovered == http.ErrAbortHandler {
						panic(recovered)
					}
					err, ok := recovered.(error)
					if !ok {
						err = fmt.Errorf("%v", recovered)
					}
					stack := []byte(nil)
					if !config.DisableStack {
						stack = make([]byte, config.StackSize)
						stack = stack[:runtime.Stack(stack, true)]
					}
					if config.HandleError != nil {
						err = config.HandleError(r, err, stack)
					}
					if err == nil {
						return
					}
					if config.DisableErrorHandler {
						returnErr = err
						return
					}
					returnErr = err
				}
			}()
			return next(r)
		}
	}
}
