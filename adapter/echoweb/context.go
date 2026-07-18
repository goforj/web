package echoweb

import (
	"context"
	"net/http"
	"sync"
	"unsafe"

	"github.com/goforj/web"
	echo "github.com/labstack/echo/v5"
)

const contextAdapterStateKey = "\x00goforj.web/echoweb.context-state"

const contextAdapterTrackedKeysKey = "\x00goforj.web/echoweb.tracked-keys"

const contextAdapterTimeoutStateKey = "\x00goforj.web/echoweb.timeout-state"

// contextAdapter is a zero-allocation view over Echo's request context.
type contextAdapter echo.Context

// echoContextProvider exposes the native context through adapter-owned wrappers.
type echoContextProvider interface {
	echoContext() *echo.Context
}

// contextAdapterState carries the uncommon Web-only state that Echo does not model.
type contextAdapterState struct {
	boundContext context.Context
	request      *http.Request
	sourceName   string
	bound        bool
}

// contextAdapterTimeoutState carries ownership metadata only for detached timeout execution.
type contextAdapterTimeoutState struct {
	fallback              *contextAdapterFallback
	bound                 bool
	previousBound         bool
	previousContext       context.Context
	request               *http.Request
	healthyRequestContext context.Context
}

// contextAdapterFallback permits safe Web Get calls against pre-detach native state until ownership ends.
type contextAdapterFallback struct {
	mu     sync.RWMutex
	source *echo.Context
}

var _ web.Context = (*contextAdapter)(nil)

// acquireContextAdapter exposes the Echo context through the Web contract without a sidecar allocation.
func acquireContextAdapter(c *echo.Context) *contextAdapter {
	return (*contextAdapter)(c)
}

// echoContext returns the native Echo view of this context.
func (c *contextAdapter) echoContext() *echo.Context {
	return (*echo.Context)(c)
}

// adapterState returns Web-only request state when a handler has opted into it.
func (c *contextAdapter) adapterState() contextAdapterState {
	if c == nil {
		return contextAdapterState{}
	}
	state, _ := c.echoContext().Get(contextAdapterStateKey).(contextAdapterState)
	return state
}

// setAdapterState stores Web-only request state on Echo's request-scoped store.
func (c *contextAdapter) setAdapterState(state contextAdapterState) {
	c.echoContext().Set(contextAdapterStateKey, state)
}

// timeoutState returns timeout-only ownership state from a detached context.
func (c *contextAdapter) timeoutState() *contextAdapterTimeoutState {
	if c == nil {
		return nil
	}
	state, _ := c.echoContext().Get(contextAdapterTimeoutStateKey).(*contextAdapterTimeoutState)
	return state
}

// setTimeoutState stores timeout-only ownership state outside the normal adapter hot path.
func (c *contextAdapter) setTimeoutState(state *contextAdapterTimeoutState) {
	c.echoContext().Set(contextAdapterTimeoutStateKey, state)
}

// Context returns the effective request context, including source-only metadata.
func (c *contextAdapter) Context() context.Context {
	state := c.adapterState()
	var base context.Context
	if state.bound {
		base = state.boundContext
	} else {
		base = c.echoContext().Request().Context()
	}
	if state.sourceName == "" {
		return base
	}
	return sourceNameContext{
		Context: base,
		source:  state.sourceName,
	}
}

// Method returns the current request method.
func (c *contextAdapter) Method() string {
	return c.echoContext().Request().Method
}

// Path returns the matched route path.
func (c *contextAdapter) Path() string {
	return c.echoContext().Path()
}

// URI returns the current request URI.
func (c *contextAdapter) URI() string {
	return c.echoContext().Request().URL.RequestURI()
}

// Scheme returns the resolved request scheme.
func (c *contextAdapter) Scheme() string {
	return c.echoContext().Scheme()
}

// Host returns the request host.
func (c *contextAdapter) Host() string {
	return c.echoContext().Request().Host
}

// Param returns a named route parameter.
func (c *contextAdapter) Param(name string) string {
	return c.echoContext().Param(name)
}

// Query returns a query parameter value.
func (c *contextAdapter) Query(name string) string {
	return c.echoContext().QueryParam(name)
}

// Header returns a request header value.
func (c *contextAdapter) Header(name string) string {
	return c.echoContext().Request().Header.Get(name)
}

// Cookie returns a named request cookie.
func (c *contextAdapter) Cookie(name string) (*http.Cookie, error) {
	return c.echoContext().Cookie(name)
}

// RealIP returns the best-effort client IP address.
func (c *contextAdapter) RealIP() string {
	return c.echoContext().RealIP()
}

// Request returns the active request, rebinding an explicit context only when needed.
func (c *contextAdapter) Request() *http.Request {
	base := c.echoContext().Request()
	state := c.adapterState()
	if !state.bound || base == nil {
		return base
	}
	if state.request == nil || state.request.Context() != state.boundContext {
		state.request = base.WithContext(state.boundContext)
		c.setAdapterState(state)
	}
	return state.request
}

// RawRequest returns the request that preceded any Web context rebinding.
func (c *contextAdapter) RawRequest() *http.Request {
	if c == nil {
		return nil
	}
	return c.echoContext().Request()
}

// SetRequest replaces the underlying request while preserving an explicit context override.
func (c *contextAdapter) SetRequest(request *http.Request) {
	state := c.adapterState()
	c.echoContext().SetRequest(request)
	if state.bound {
		state.request = nil
		c.setAdapterState(state)
	}
}

// SetContext overrides the effective request context for subsequent Request and Context calls.
func (c *contextAdapter) SetContext(ctx context.Context) {
	state := c.adapterState()
	if ctx == nil {
		state.boundContext = nil
		state.request = nil
		state.sourceName = ""
		state.bound = false
		c.clearTimeoutBinding()
		c.setAdapterState(state)
		return
	}
	state.boundContext = ctx
	state.request = nil
	state.sourceName = ""
	state.bound = true
	c.clearTimeoutBinding()
	c.setAdapterState(state)
}

// BindTimeoutRequest installs a derived request context while retaining enough state to restore the healthy request.
func (c *contextAdapter) BindTimeoutRequest(request *http.Request, ctx context.Context) {
	state := c.adapterState()
	timeoutState := c.timeoutState()
	if timeoutState == nil {
		timeoutState = &contextAdapterTimeoutState{}
		c.setTimeoutState(timeoutState)
	}
	currentRequest := c.echoContext().Request()
	timeoutState.previousBound = state.bound
	timeoutState.previousContext = state.boundContext
	if currentRequest != nil {
		timeoutState.healthyRequestContext = currentRequest.Context()
	}
	timeoutState.request = request
	timeoutState.bound = true
	state.boundContext = ctx
	state.request = request
	state.bound = true
	c.echoContext().SetRequest(request)
	c.setAdapterState(state)
}

// RestoreTimeoutRequest removes an adapter-owned deadline without discarding handler-request mutations.
func (c *contextAdapter) RestoreTimeoutRequest() {
	state := c.adapterState()
	timeoutState := c.timeoutState()
	if timeoutState == nil || !timeoutState.bound {
		return
	}
	currentRequest := c.echoContext().Request()
	if currentRequest != nil && currentRequest == timeoutState.request && timeoutState.healthyRequestContext != nil {
		currentRequest = currentRequest.WithContext(timeoutState.healthyRequestContext)
		c.echoContext().SetRequest(currentRequest)
	}
	state.bound = timeoutState.previousBound
	state.boundContext = timeoutState.previousContext
	state.request = nil
	c.clearTimeoutBinding()
	c.setAdapterState(state)
}

// clearTimeoutBinding removes adapter-only deadline restoration state.
func (c *contextAdapter) clearTimeoutBinding() {
	state := c.timeoutState()
	if state == nil {
		return
	}
	state.bound = false
	state.previousBound = false
	state.previousContext = nil
	state.request = nil
	state.healthyRequestContext = nil
}

// SetAppSourceName records the application source name on the adapter fast path.
func (c *contextAdapter) SetAppSourceName(source string) {
	state := c.adapterState()
	state.sourceName = source
	c.setAdapterState(state)
}

// AppSourceName returns the application source name attached to the request.
func (c *contextAdapter) AppSourceName() string {
	return c.adapterState().sourceName
}

// Response returns the response adapter for the current request.
func (c *contextAdapter) Response() web.Response {
	return (*responseAdapter)(c)
}

// ResponseWriter returns the active response writer.
func (c *contextAdapter) ResponseWriter() http.ResponseWriter {
	return c.echoContext().Response()
}

// SetResponseWriter replaces the active response writer.
func (c *contextAdapter) SetResponseWriter(writer http.ResponseWriter) {
	c.echoContext().SetResponse(writer)
}

// Bind decodes the request body into target using Echo's binder.
func (c *contextAdapter) Bind(target any) error {
	return c.echoContext().Bind(target)
}

// Set stores request-scoped data on the underlying Echo context.
func (c *contextAdapter) Set(key string, value any) {
	native := c.echoContext()
	native.Set(key, value)
	trackContextAdapterKey(native, key)
}

// Get reads request-scoped data from the underlying Echo context.
func (c *contextAdapter) Get(key string) any {
	value := c.echoContext().Get(key)
	if value != nil || contextAdapterKeyTracked(c.echoContext(), key) {
		return value
	}
	timeoutState := c.timeoutState()
	if timeoutState == nil || timeoutState.fallback == nil {
		return nil
	}
	value, active := timeoutState.fallback.get(key)
	if !active {
		return nil
	}
	c.Set(key, value)
	return value
}

// contextAdapterKeyTracked reports whether a nil value was explicitly stored through the Web contract.
func contextAdapterKeyTracked(native *echo.Context, key string) bool {
	switch tracked := native.Get(contextAdapterTrackedKeysKey).(type) {
	case string:
		return tracked == key
	case []string:
		for _, trackedKey := range tracked {
			if trackedKey == key {
				return true
			}
		}
	}
	return false
}

// AddHeader appends a response header value.
func (c *contextAdapter) AddHeader(name string, value string) {
	c.echoContext().Response().Header().Add(name, value)
}

// SetHeader sets a response header value.
func (c *contextAdapter) SetHeader(name string, value string) {
	c.echoContext().Response().Header().Set(name, value)
}

// SetCookie writes a response cookie.
func (c *contextAdapter) SetCookie(cookie *http.Cookie) {
	http.SetCookie(c.echoContext().Response(), cookie)
}

// JSON writes a JSON response.
func (c *contextAdapter) JSON(code int, payload any) error {
	native := c.echoContext()
	setResponseContentType(native.Response(), echo.MIMEApplicationJSON)
	response, err := echo.UnwrapResponse(native.Response())
	if err != nil {
		return native.JSON(code, payload)
	}
	response.Status = code
	return native.Echo().JSONSerializer.Serialize(native, payload, "")
}

// Blob writes a raw byte response with the provided content type.
func (c *contextAdapter) Blob(code int, contentType string, body []byte) error {
	return writeResponseBlob(c.echoContext(), code, contentType, body)
}

// File serves a file from disk.
func (c *contextAdapter) File(path string) error {
	http.ServeFile(c.echoContext().Response(), c.echoContext().Request(), path)
	return nil
}

// Text writes a plain-text response.
func (c *contextAdapter) Text(code int, body string) error {
	return writeResponseBlob(c.echoContext(), code, echo.MIMETextPlainCharsetUTF8, immutableStringBytes(body))
}

// HTML writes an HTML response.
func (c *contextAdapter) HTML(code int, body string) error {
	return writeResponseBlob(c.echoContext(), code, echo.MIMETextHTMLCharsetUTF8, immutableStringBytes(body))
}

// NoContent writes an empty response with the provided status code.
func (c *contextAdapter) NoContent(code int) error {
	return c.echoContext().NoContent(code)
}

// Redirect sends an HTTP redirect response.
func (c *contextAdapter) Redirect(code int, url string) error {
	return c.echoContext().Redirect(code, url)
}

// StatusCode returns the response status code.
func (c *contextAdapter) StatusCode() int {
	return (*responseAdapter)(c).StatusCode()
}

// Native returns the underlying Echo context.
func (c *contextAdapter) Native() any {
	return c.echoContext()
}

// DisableReuse is retained for middleware compatibility; detached contexts now provide lifecycle isolation.
func (c *contextAdapter) DisableReuse() {}

// DetachedContext creates an independently owned request context and a synchronous commit callback.
// Echo does not expose a race-safe native-store snapshot, so cross-boundary
// state is bridged and committed only through the Web Set and Get contract.
func (c *contextAdapter) DetachedContext() (web.Context, func()) {
	if c == nil {
		return c, func() {}
	}

	source := c.echoContext()
	engine := source.Echo()
	if engine == nil {
		engine = echo.New()
	}

	request := c.Request()
	if request != nil {
		request = request.Clone(c.Context())
	}
	detachedNative := engine.NewContext(request, c.ResponseWriter())
	route := source.RouteInfo()
	pathValues := append(make(echo.PathValues, 0, len(source.PathValues())), source.PathValues()...)
	detachedNative.InitializeRoute(&route, &pathValues)
	detachedNative.SetPath(source.Path())
	detachedNative.SetLogger(source.Logger())

	state := c.adapterState()
	state.request = nil
	detached := acquireContextAdapter(detachedNative)
	detached.setAdapterState(state)
	detached.setTimeoutState(&contextAdapterTimeoutState{
		fallback: &contextAdapterFallback{source: source},
	})
	copyTrackedContextAdapterValues(source, detachedNative)

	return detached, func() {
		detached.commitTo(c)
	}
}

// commitTo synchronizes handler-visible context changes after detached work completes before its deadline.
func (c *contextAdapter) commitTo(target *contextAdapter) {
	if c == nil || target == nil {
		return
	}

	c.ReleaseDetachedContext()
	source := c.echoContext()
	destination := target.echoContext()
	destination.SetRequest(c.Request())
	state := c.adapterState()
	state.request = nil
	target.setAdapterState(state)
	copyTrackedContextAdapterValues(source, destination)

	route := source.RouteInfo()
	pathValues := append(make(echo.PathValues, 0, len(source.PathValues())), source.PathValues()...)
	destination.InitializeRoute(&route, &pathValues)
	destination.SetPath(source.Path())
	destination.SetLogger(source.Logger())
}

// trackContextAdapterKey records Web-visible values without allocating for the common single-key case.
func trackContextAdapterKey(native *echo.Context, key string) {
	if key == contextAdapterStateKey || key == contextAdapterTrackedKeysKey || key == contextAdapterTimeoutStateKey {
		return
	}

	switch tracked := native.Get(contextAdapterTrackedKeysKey).(type) {
	case nil:
		native.Set(contextAdapterTrackedKeysKey, key)
	case string:
		if tracked != key {
			native.Set(contextAdapterTrackedKeysKey, []string{tracked, key})
		}
	case []string:
		for _, trackedKey := range tracked {
			if trackedKey == key {
				return
			}
		}
		native.Set(contextAdapterTrackedKeysKey, append(tracked, key))
	}
}

// copyTrackedContextAdapterValues preserves values written through the Web context contract.
func copyTrackedContextAdapterValues(source *echo.Context, destination *echo.Context) {
	tracked := source.Get(contextAdapterTrackedKeysKey)
	switch keys := tracked.(type) {
	case string:
		destination.Set(keys, source.Get(keys))
	case []string:
		for _, key := range keys {
			destination.Set(key, source.Get(key))
		}
		tracked = append([]string(nil), keys...)
	default:
		return
	}
	destination.Set(contextAdapterTrackedKeysKey, tracked)
}

// get reads an original native value only while the detached request still owns that source.
func (f *contextAdapterFallback) get(key string) (any, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.source == nil {
		return nil, false
	}
	return f.source.Get(key), true
}

// close prevents late handlers from consulting an Echo context that is about to be reused.
func (f *contextAdapterFallback) close() {
	f.mu.Lock()
	f.source = nil
	f.mu.Unlock()
}

// ReleaseDetachedContext severs access to the original Echo context before its request ownership ends.
func (c *contextAdapter) ReleaseDetachedContext() {
	state := c.timeoutState()
	if state != nil && state.fallback != nil {
		state.fallback.close()
	}
}

// immutableStringBytes creates a read-only byte view for the duration of a response write.
func immutableStringBytes(value string) []byte {
	// io.Writer implementations may neither retain nor mutate the supplied bytes,
	// which lets Text and HTML avoid copying an immutable string before Echo writes it.
	return unsafe.Slice(unsafe.StringData(value), len(value))
}

// setResponseContentType matches Echo's first-value semantics without repeatedly canonicalizing a constant header name.
func setResponseContentType(writer http.ResponseWriter, value string) {
	header := writer.Header()
	if values := header[echo.HeaderContentType]; len(values) == 0 || values[0] == "" {
		header[echo.HeaderContentType] = []string{value}
	}
}

// writeResponseBlob preserves Echo's response lifecycle while avoiding its generic header canonicalization path.
func writeResponseBlob(native *echo.Context, code int, contentType string, body []byte) error {
	setResponseContentType(native.Response(), contentType)
	native.Response().WriteHeader(code)
	_, err := native.Response().Write(body)
	return err
}

// responseAdapter is a zero-allocation response view over Echo's request context.
type responseAdapter contextAdapter

// sourceNameContext carries application source metadata without cloning a request.
type sourceNameContext struct {
	context.Context
	source string
}

// AppSourceName returns the app source name carried by this lightweight context wrapper.
func (c sourceNameContext) AppSourceName() string {
	return c.source
}

var _ web.Response = (*responseAdapter)(nil)

// echoContext returns the native Echo context that owns this response.
func (r *responseAdapter) echoContext() *echo.Context {
	return (*echo.Context)(r)
}

// Header returns the response header map.
func (r *responseAdapter) Header() http.Header {
	return r.echoContext().Response().Header()
}

// Writer returns the active response writer.
func (r *responseAdapter) Writer() http.ResponseWriter {
	return r.echoContext().Response()
}

// SetWriter replaces the active response writer.
func (r *responseAdapter) SetWriter(writer http.ResponseWriter) {
	r.echoContext().SetResponse(writer)
}

// StatusCode returns the committed response status code.
func (r *responseAdapter) StatusCode() int {
	response := r.currentResponse()
	if response == nil {
		return 0
	}
	return response.Status
}

// Size returns the number of bytes written to the response.
func (r *responseAdapter) Size() int64 {
	response := r.currentResponse()
	if response == nil {
		return 0
	}
	return response.Size
}

// Committed reports whether the response has been committed.
func (r *responseAdapter) Committed() bool {
	response := r.currentResponse()
	if response == nil {
		return false
	}
	return response.Committed
}

// Native returns the underlying Echo response writer.
func (r *responseAdapter) Native() any {
	if r == nil {
		return nil
	}
	return r.echoContext().Response()
}

// currentResponse unwraps the active writer to Echo's response state.
func (r *responseAdapter) currentResponse() *echo.Response {
	if r == nil {
		return nil
	}
	response, err := echo.UnwrapResponse(r.echoContext().Response())
	if err != nil {
		return nil
	}
	return response
}

// UnwrapContext returns the underlying Echo context when the web.Context came from this adapter.
// @group Adapter
// Example:
// adapter := echoweb.New()
//
//	adapter.Router().GET("/healthz", func(c web.Context) error {
//		_, ok := echoweb.UnwrapContext(c)
//		fmt.Println(ok)
//		return c.NoContent(http.StatusOK)
//	})
//
// rr := httptest.NewRecorder()
// req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
// adapter.ServeHTTP(rr, req)
//
//	// true
func UnwrapContext(ctx web.Context) (*echo.Context, bool) {
	provider, ok := ctx.(echoContextProvider)
	if !ok || provider == nil {
		return nil, false
	}
	native := provider.echoContext()
	return native, native != nil
}
