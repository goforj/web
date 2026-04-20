package webmiddleware

import (
	"errors"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"sync"
	"time"

	"github.com/goforj/web"
)

// ProxyTarget is an upstream target.
type ProxyTarget struct {
	Name string
	URL  *url.URL
	Meta map[string]any
}

// ProxyBalancer selects upstream targets.
type ProxyBalancer interface {
	AddTarget(*ProxyTarget) bool
	RemoveTarget(string) bool
	Next(web.Context) *ProxyTarget
}

type commonBalancer struct {
	targets []*ProxyTarget
	mutex   sync.Mutex
}

type randomBalancer struct {
	commonBalancer
	random *rand.Rand
}

type roundRobinBalancer struct {
	commonBalancer
	index int
}

// NewRandomBalancer creates a random proxy balancer.
// @group Middleware - Proxying
// Example:
// target, _ := url.Parse("http://localhost:8080")
// balancer := webmiddleware.NewRandomBalancer([]*webmiddleware.ProxyTarget{{URL: target}})
// fmt.Println(balancer.Next(nil).URL.Host)
//	// localhost:8080
func NewRandomBalancer(targets []*ProxyTarget) ProxyBalancer {
	return &randomBalancer{
		commonBalancer: commonBalancer{targets: targets},
		random:         rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// NewRoundRobinBalancer creates a round-robin proxy balancer.
// @group Middleware - Proxying
// Example:
// target, _ := url.Parse("http://localhost:8080")
// balancer := webmiddleware.NewRoundRobinBalancer([]*webmiddleware.ProxyTarget{{URL: target}})
// fmt.Println(balancer.Next(nil).URL.Host)
//	// localhost:8080
func NewRoundRobinBalancer(targets []*ProxyTarget) ProxyBalancer {
	return &roundRobinBalancer{commonBalancer: commonBalancer{targets: targets}}
}

func (b *commonBalancer) AddTarget(target *ProxyTarget) bool {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	for _, existing := range b.targets {
		if existing.Name == target.Name {
			return false
		}
	}
	b.targets = append(b.targets, target)
	return true
}

func (b *commonBalancer) RemoveTarget(name string) bool {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	for i, target := range b.targets {
		if target.Name == name {
			b.targets = append(b.targets[:i], b.targets[i+1:]...)
			return true
		}
	}
	return false
}

func (b *randomBalancer) Next(_ web.Context) *ProxyTarget {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if len(b.targets) == 0 {
		return nil
	}
	if len(b.targets) == 1 {
		return b.targets[0]
	}
	return b.targets[b.random.Intn(len(b.targets))]
}

func (b *roundRobinBalancer) Next(_ web.Context) *ProxyTarget {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if len(b.targets) == 0 {
		return nil
	}
	if b.index >= len(b.targets) {
		b.index = 0
	}
	target := b.targets[b.index]
	b.index++
	return target
}

// ProxyConfig configures reverse proxy behavior.
type ProxyConfig struct {
	Skipper        Skipper
	Balancer       ProxyBalancer
	ErrorHandler   func(web.Context, error) error
	Rewrite        map[string]string
	RegexRewrite   map[*regexp.Regexp]string
	ContextKey     string
	Transport      http.RoundTripper
	ModifyResponse func(*http.Response) error
}

// DefaultProxyConfig is the default proxy config.
var DefaultProxyConfig = ProxyConfig{
	Skipper:    DefaultSkipper,
	ContextKey: "target",
}

// Proxy creates a proxy middleware.
// @group Middleware - Proxying
// Example:
// target, _ := url.Parse("http://localhost:8080")
// balancer := webmiddleware.NewRandomBalancer([]*webmiddleware.ProxyTarget{{URL: target}})
// req := httptest.NewRequest(http.MethodGet, "/", nil)
// ctx := webtest.NewContext(req, nil, "/", nil)
// _ = webmiddleware.Proxy(balancer)(func(c web.Context) error { return nil })(ctx)
// fmt.Println(ctx.Get("target").(*webmiddleware.ProxyTarget).URL.Host)
//	// localhost:8080
func Proxy(balancer ProxyBalancer) web.Middleware {
	config := DefaultProxyConfig
	config.Balancer = balancer
	return ProxyWithConfig(config)
}

// ProxyWithConfig creates a proxy middleware with config.
// @group Middleware - Proxying
// Example:
// target, _ := url.Parse("http://localhost:8080")
// mw := webmiddleware.ProxyWithConfig(webmiddleware.ProxyConfig{
// 	Balancer: webmiddleware.NewRandomBalancer([]*webmiddleware.ProxyTarget{{URL: target}}),
// })
// req := httptest.NewRequest(http.MethodGet, "/old/path", nil)
// ctx := webtest.NewContext(req, nil, "/", nil)
// _ = mw(func(c web.Context) error { return nil })(ctx)
// fmt.Println(ctx.Get("target").(*webmiddleware.ProxyTarget).URL.Host)
//	// localhost:8080
func ProxyWithConfig(config ProxyConfig) web.Middleware {
	if config.Balancer == nil {
		panic("web: proxy middleware requires a balancer")
	}
	if config.Skipper == nil {
		config.Skipper = DefaultProxyConfig.Skipper
	}
	if config.ErrorHandler == nil {
		config.ErrorHandler = func(r web.Context, err error) error {
			return err
		}
	}
	if config.ContextKey == "" {
		config.ContextKey = DefaultProxyConfig.ContextKey
	}
	regexRewrite := map[*regexp.Regexp]string{}
	for pattern, target := range wildcardRewriteRules(config.Rewrite) {
		regexRewrite[pattern] = target
	}
	for pattern, target := range config.RegexRewrite {
		regexRewrite[pattern] = target
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			if config.Skipper(r) {
				return next(r)
			}

			target := config.Balancer.Next(r)
			if target == nil || target.URL == nil {
				return config.ErrorHandler(r, errors.New("proxy target unavailable"))
			}
			r.Set(config.ContextKey, target)

			req := r.Request()
			if err := rewriteRequest(regexRewrite, req); err != nil {
				return config.ErrorHandler(r, err)
			}

			proxy := &httputil.ReverseProxy{
				Transport:      config.Transport,
				ModifyResponse: config.ModifyResponse,
				Director: func(out *http.Request) {
					out.URL.Scheme = target.URL.Scheme
					out.URL.Host = target.URL.Host
					out.Host = target.URL.Host
					out.URL.Path = req.URL.Path
					out.URL.RawQuery = req.URL.RawQuery
					out.RequestURI = ""
					out.Header.Set("X-Real-IP", r.RealIP())
					if out.Header.Get("X-Forwarded-Proto") == "" {
						out.Header.Set("X-Forwarded-Proto", r.Scheme())
					}
				},
				ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
					if handledErr := config.ErrorHandler(r, err); handledErr != nil {
						http.Error(rw, handledErr.Error(), http.StatusBadGateway)
						return
					}
					http.Error(rw, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
				},
			}

			proxy.ServeHTTP(r.ResponseWriter(), req)
			return nil
		}
	}
}
