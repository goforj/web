package webmiddleware

import (
	"errors"
	"math"
	"sync"
	"time"

	"github.com/goforj/web"
	"golang.org/x/time/rate"
)

var (
	// ErrRateLimitExceeded is returned when the rate limit is exceeded.
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
	// ErrRateLimitIdentifier is returned when extracting a rate limit identifier fails.
	ErrRateLimitIdentifier = errors.New("error while extracting identifier")
)

// RateLimiterStore is the store interface for the rate limiter.
type RateLimiterStore interface {
	Allow(identifier string) (bool, error)
}

// Extractor extracts a value from the request context.
type Extractor func(web.Context) (string, error)

// RateLimiterConfig configures rate limiting.
type RateLimiterConfig struct {
	Skipper             Skipper
	IdentifierExtractor Extractor
	Store               RateLimiterStore
	ErrorHandler        func(web.Context, error) error
	DenyHandler         func(web.Context, string, error) error
}

// DefaultRateLimiterConfig is the default rate limiter config.
var DefaultRateLimiterConfig = RateLimiterConfig{
	Skipper: DefaultSkipper,
	IdentifierExtractor: func(r web.Context) (string, error) {
		return r.RealIP(), nil
	},
	ErrorHandler: func(r web.Context, err error) error {
		return r.JSON(403, map[string]any{
			"error": ErrRateLimitIdentifier.Error(),
		})
	},
	DenyHandler: func(r web.Context, _ string, err error) error {
		return r.JSON(429, map[string]any{
			"error": ErrRateLimitExceeded.Error(),
		})
	},
}

// RateLimiter creates a rate limiting middleware.
// @group Middleware
// Example:
// store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
// handler := webmiddleware.RateLimiter(store)(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
// req1 := httptest.NewRequest(http.MethodGet, "/", nil)
// req1.RemoteAddr = "192.0.2.10:1234"
// ctx1 := webtest.NewContext(req1, nil, "/", nil)
// _ = handler(ctx1)
// req2 := httptest.NewRequest(http.MethodGet, "/", nil)
// req2.RemoteAddr = "192.0.2.10:1234"
// ctx2 := webtest.NewContext(req2, nil, "/", nil)
// _ = handler(ctx2)
// fmt.Println(ctx1.StatusCode(), ctx2.StatusCode())
//	// 204 429
func RateLimiter(store RateLimiterStore) web.Middleware {
	config := DefaultRateLimiterConfig
	config.Store = store
	return RateLimiterWithConfig(config)
}

// RateLimiterWithConfig creates a rate limiting middleware with config.
// @group Middleware
// Example:
// store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
// mw := webmiddleware.RateLimiterWithConfig(webmiddleware.RateLimiterConfig{Store: store})
// ctx := webtest.NewContext(nil, nil, "/", nil)
// handler := mw(func(c web.Context) error { return c.NoContent(http.StatusAccepted) })
// _ = handler(ctx)
// fmt.Println(ctx.StatusCode())
//	// 202
func RateLimiterWithConfig(config RateLimiterConfig) web.Middleware {
	if config.Skipper == nil {
		config.Skipper = DefaultRateLimiterConfig.Skipper
	}
	if config.IdentifierExtractor == nil {
		config.IdentifierExtractor = DefaultRateLimiterConfig.IdentifierExtractor
	}
	if config.ErrorHandler == nil {
		config.ErrorHandler = DefaultRateLimiterConfig.ErrorHandler
	}
	if config.DenyHandler == nil {
		config.DenyHandler = DefaultRateLimiterConfig.DenyHandler
	}
	if config.Store == nil {
		panic("web: rate limiter store is required")
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			if config.Skipper(r) {
				return next(r)
			}

			identifier, err := config.IdentifierExtractor(r)
			if err != nil {
				return config.ErrorHandler(r, err)
			}

			allowed, err := config.Store.Allow(identifier)
			if !allowed {
				return config.DenyHandler(r, identifier, err)
			}
			return next(r)
		}
	}
}

// RateLimiterMemoryStoreConfig configures the in-memory rate limiter store.
type RateLimiterMemoryStoreConfig struct {
	Rate      rate.Limit
	Burst     int
	ExpiresIn time.Duration
}

// DefaultRateLimiterMemoryStoreConfig is the default in-memory store config.
var DefaultRateLimiterMemoryStoreConfig = RateLimiterMemoryStoreConfig{
	ExpiresIn: 3 * time.Minute,
}

// RateLimiterMemoryStore is an in-memory store for rate limiting.
type RateLimiterMemoryStore struct {
	visitors map[string]*visitor
	mutex    sync.Mutex

	rate        rate.Limit
	burst       int
	expiresIn   time.Duration
	lastCleanup time.Time
	timeNow     func() time.Time
}

type visitor struct {
	*rate.Limiter
	lastSeen time.Time
}

// NewRateLimiterMemoryStore creates an in-memory rate limiter store.
// @group Middleware
// Example:
// store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
// allowed1, _ := store.Allow("192.0.2.1")
// allowed2, _ := store.Allow("192.0.2.1")
// fmt.Println(allowed1, allowed2)
//	// true false
func NewRateLimiterMemoryStore(limit rate.Limit) *RateLimiterMemoryStore {
	return NewRateLimiterMemoryStoreWithConfig(RateLimiterMemoryStoreConfig{Rate: limit})
}

// NewRateLimiterMemoryStoreWithConfig creates an in-memory rate limiter store with config.
// @group Middleware
// Example:
// store := webmiddleware.NewRateLimiterMemoryStoreWithConfig(webmiddleware.RateLimiterMemoryStoreConfig{Rate: rate.Every(time.Second)})
// allowed, _ := store.Allow("192.0.2.1")
// fmt.Println(allowed)
//	// true
func NewRateLimiterMemoryStoreWithConfig(config RateLimiterMemoryStoreConfig) *RateLimiterMemoryStore {
	store := &RateLimiterMemoryStore{
		rate:      config.Rate,
		burst:     config.Burst,
		expiresIn: config.ExpiresIn,
		visitors:  map[string]*visitor{},
		timeNow:   time.Now,
	}
	if store.expiresIn == 0 {
		store.expiresIn = DefaultRateLimiterMemoryStoreConfig.ExpiresIn
	}
	if store.burst == 0 {
		store.burst = int(math.Max(1, math.Ceil(float64(store.rate))))
	}
	store.lastCleanup = store.timeNow()
	return store
}

// Allow checks whether the given identifier is allowed through.
// @group Middleware
// Example:
// store := webmiddleware.NewRateLimiterMemoryStore(rate.Every(time.Second))
// allowed, err := store.Allow("127.0.0.1")
// fmt.Println(err == nil, allowed)
//	// true true
func (store *RateLimiterMemoryStore) Allow(identifier string) (bool, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	limiter, exists := store.visitors[identifier]
	if !exists {
		limiter = &visitor{
			Limiter: rate.NewLimiter(store.rate, store.burst),
		}
		store.visitors[identifier] = limiter
	}

	now := store.timeNow()
	limiter.lastSeen = now
	if now.Sub(store.lastCleanup) > store.expiresIn {
		store.cleanupStaleVisitors()
	}

	return limiter.AllowN(now, 1), nil
}

func (store *RateLimiterMemoryStore) cleanupStaleVisitors() {
	now := store.timeNow()
	for identifier, limiter := range store.visitors {
		if now.Sub(limiter.lastSeen) > store.expiresIn {
			delete(store.visitors, identifier)
		}
	}
	store.lastCleanup = now
}
