package webprometheus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/goforj/web"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/expfmt"
)

const (
	defaultNamespace = "web"
)

const (
	_           = iota
	bKB float64 = 1 << (10 * iota)
	bMB
)

var defaultSizeBuckets = []float64{1.0 * bKB, 2.0 * bKB, 5.0 * bKB, 10.0 * bKB, 100 * bKB, 500 * bKB, 1.0 * bMB, 2.5 * bMB, 5.0 * bMB, 10.0 * bMB}

// LabelValueFunc resolves a label value from request context and handler error.
type LabelValueFunc func(web.Context, error) string

// Config configures Prometheus middleware and scraping behavior.
type Config struct {
	Skipper                   func(web.Context) bool
	Namespace                 string
	Subsystem                 string
	LabelFuncs                map[string]LabelValueFunc
	HistogramOptsFunc         func(prometheus.HistogramOpts) prometheus.HistogramOpts
	CounterOptsFunc           func(prometheus.CounterOpts) prometheus.CounterOpts
	Registerer                prometheus.Registerer
	Gatherer                  prometheus.Gatherer
	BeforeNext                func(web.Context)
	AfterNext                 func(web.Context, error)
	DoNotUseRequestPathFor404 bool
	DurationBuckets           []float64
	SizeBuckets               []float64
	RequestSizeBuckets        []float64
	DisableCompression        bool
	timeNow                   func() time.Time
}

// PushGatewayConfig contains the configuration for pushing to a Prometheus push gateway.
type PushGatewayConfig struct {
	PushGatewayURL  string
	PushInterval    time.Duration
	Gatherer        prometheus.Gatherer
	ErrorHandler    func(error) error
	ClientTransport http.RoundTripper
}

// Metrics records Prometheus metrics for HTTP traffic and exposes a scrape handler.
type Metrics struct {
	config          Config
	gatherer        prometheus.Gatherer
	labelNames      []string
	customValuers   []customLabelValuer
	inFlight        *prometheus.GaugeVec
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	responseSize    *prometheus.HistogramVec
	requestSize     *prometheus.HistogramVec
}

type customLabelValuer struct {
	index     int
	label     string
	valueFunc LabelValueFunc
}

var (
	defaultOnce    sync.Once
	defaultMetrics *Metrics
)

// Default returns the package-level Prometheus metrics instance.
// @group Prometheus
// Example:
// _ = webprometheus.Default()
func Default() *Metrics {
	defaultOnce.Do(func() {
		defaultMetrics = MustNew(Config{})
	})
	return defaultMetrics
}

// Middleware returns the package-level Prometheus middleware.
// @group Prometheus
// Example:
// _ = webprometheus.Middleware()
func Middleware() web.Middleware {
	return Default().Middleware()
}

// Handler returns the package-level Prometheus scrape handler.
// @group Prometheus
// Example:
// _ = webprometheus.Handler()
func Handler() web.Handler {
	return Default().Handler()
}

// MustNew creates a Metrics instance and panics on registration errors.
// @group Prometheus
// Example:
// _ = webprometheus.MustNew(webprometheus.Config{})
func MustNew(config Config) *Metrics {
	metrics, err := New(config)
	if err != nil {
		panic(err)
	}
	return metrics
}

// New creates a Metrics instance backed by Prometheus collectors.
// @group Prometheus
// Example:
// metrics, err := webprometheus.New(webprometheus.Config{Namespace: "app"})
// _ = metrics
// fmt.Println(err == nil)
//	// true
func New(config Config) (*Metrics, error) {
	config = withDefaults(config)

	labelNames, customValuers := createLabels(config.LabelFuncs)

	inFlight, err := registerCollector(config.Registerer, prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: config.Namespace,
			Subsystem: config.Subsystem,
			Name:      "requests_in_flight",
			Help:      "Current number of in-flight HTTP requests.",
		},
		[]string{"method", "url"},
	))
	if err != nil {
		return nil, err
	}

	requestsTotal, err := registerCollector(config.Registerer, prometheus.NewCounterVec(
		config.CounterOptsFunc(prometheus.CounterOpts{
			Namespace: config.Namespace,
			Subsystem: config.Subsystem,
			Name:      "requests_total",
			Help:      "How many HTTP requests processed, partitioned by status code and HTTP method.",
		}),
		labelNames,
	))
	if err != nil {
		return nil, err
	}

	requestDuration, err := registerCollector(config.Registerer, prometheus.NewHistogramVec(
		config.HistogramOptsFunc(prometheus.HistogramOpts{
			Namespace: config.Namespace,
			Subsystem: config.Subsystem,
			Name:      "request_duration_seconds",
			Help:      "The HTTP request latencies in seconds.",
			Buckets:   bucketsOrDefault(config.DurationBuckets, prometheus.DefBuckets),
		}),
		labelNames,
	))
	if err != nil {
		return nil, err
	}

	responseSize, err := registerCollector(config.Registerer, prometheus.NewHistogramVec(
		config.HistogramOptsFunc(prometheus.HistogramOpts{
			Namespace: config.Namespace,
			Subsystem: config.Subsystem,
			Name:      "response_size_bytes",
			Help:      "The HTTP response sizes in bytes.",
			Buckets:   bucketsOrDefault(config.SizeBuckets, defaultSizeBuckets),
		}),
		labelNames,
	))
	if err != nil {
		return nil, err
	}

	requestSize, err := registerCollector(config.Registerer, prometheus.NewHistogramVec(
		config.HistogramOptsFunc(prometheus.HistogramOpts{
			Namespace: config.Namespace,
			Subsystem: config.Subsystem,
			Name:      "request_size_bytes",
			Help:      "The HTTP request sizes in bytes.",
			Buckets:   bucketsOrDefault(config.RequestSizeBuckets, defaultSizeBuckets),
		}),
		labelNames,
	))
	if err != nil {
		return nil, err
	}

	return &Metrics{
		config:          config,
		gatherer:        config.Gatherer,
		labelNames:      labelNames,
		customValuers:   customValuers,
		inFlight:        inFlight,
		requestsTotal:   requestsTotal,
		requestDuration: requestDuration,
		responseSize:    responseSize,
		requestSize:     requestSize,
	}, nil
}

// Middleware records Prometheus metrics for each request.
// @group Prometheus
// Example:
// metrics, _ := webprometheus.New(webprometheus.Config{})
// _ = metrics.Middleware()
func (m *Metrics) Middleware() web.Middleware {
	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			if m.config.Skipper != nil && m.config.Skipper(r) {
				return next(r)
			}

			if m.config.BeforeNext != nil {
				m.config.BeforeNext(r)
			}

			url := routeLabel(r, m.config.DoNotUseRequestPathFor404)
			method := r.Method()
			m.inFlight.WithLabelValues(method, url).Inc()
			defer m.inFlight.WithLabelValues(method, url).Dec()

			reqSize := computeApproximateRequestSize(r.Request())
			start := m.config.timeNow()
			err := next(r)
			elapsed := m.config.timeNow().Sub(start).Seconds()

			if m.config.AfterNext != nil {
				m.config.AfterNext(r, err)
			}

			values := make([]string, len(m.labelNames))
			values[0] = strconv.Itoa(normalizeStatus(r.Response().Committed(), r.StatusCode(), r.Path(), err))
			values[1] = method
			values[2] = r.Request().Host
			values[3] = strings.ToValidUTF8(url, "\uFFFD")
			for _, cv := range m.customValuers {
				values[cv.index] = cv.valueFunc(r, err)
			}

			m.requestDuration.WithLabelValues(values...).Observe(elapsed)
			m.requestsTotal.WithLabelValues(values...).Inc()
			m.requestSize.WithLabelValues(values...).Observe(float64(reqSize))
			m.responseSize.WithLabelValues(values...).Observe(float64(normalizeSize(r.Response().Size())))

			return err
		}
	}
}

// Handler exposes the configured Prometheus metrics as a web.Handler.
// @group Prometheus
// Example:
// metrics, _ := webprometheus.New(webprometheus.Config{})
// _ = metrics.Handler()
func (m *Metrics) Handler() web.Handler {
	inner := promhttp.HandlerFor(m.gatherer, promhttp.HandlerOpts{
		DisableCompression: m.config.DisableCompression,
	})
	if r, ok := m.gatherer.(prometheus.Registerer); ok {
		inner = promhttp.InstrumentMetricHandler(r, inner)
	}
	return func(ctx web.Context) error {
		inner.ServeHTTP(ctx.ResponseWriter(), ctx.Request())
		return nil
	}
}

// RunPushGatewayGatherer starts pushing collected metrics until the context finishes.
// @group Prometheus
// Example:
// err := webprometheus.RunPushGatewayGatherer(context.Background(), webprometheus.PushGatewayConfig{})
// fmt.Println(err != nil)
//	// true
func RunPushGatewayGatherer(ctx context.Context, config PushGatewayConfig) error {
	if config.PushGatewayURL == "" {
		return errors.New("push gateway URL is missing")
	}
	if config.PushInterval <= 0 {
		config.PushInterval = time.Minute
	}
	if config.Gatherer == nil {
		config.Gatherer = prometheus.DefaultGatherer
	}
	if config.ErrorHandler == nil {
		config.ErrorHandler = func(err error) error { return nil }
	}

	client := &http.Client{Transport: config.ClientTransport}
	out := &bytes.Buffer{}
	ticker := time.NewTicker(config.PushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			out.Reset()
			if err := WriteGatheredMetrics(out, config.Gatherer); err != nil {
				if hErr := config.ErrorHandler(fmt.Errorf("failed to create metrics: %w", err)); hErr != nil {
					return hErr
				}
				continue
			}

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.PushGatewayURL, out)
			if err != nil {
				if hErr := config.ErrorHandler(fmt.Errorf("failed to create push gateway request: %w", err)); hErr != nil {
					return hErr
				}
				continue
			}
			res, err := client.Do(req)
			if err != nil {
				if hErr := config.ErrorHandler(fmt.Errorf("error sending to push gateway: %w", err)); hErr != nil {
					return hErr
				}
				continue
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusOK {
				if hErr := config.ErrorHandler(fmt.Errorf("post metrics request did not succeed: %d", res.StatusCode)); hErr != nil {
					return hErr
				}
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// WriteGatheredMetrics gathers collected metrics and writes them to the given writer.
// @group Prometheus
// Example:
// var buf bytes.Buffer
// err := webprometheus.WriteGatheredMetrics(&buf, prometheus.NewRegistry())
// fmt.Println(err == nil)
//	// true
func WriteGatheredMetrics(writer io.Writer, gatherer prometheus.Gatherer) error {
	metricFamilies, err := gatherer.Gather()
	if err != nil {
		return err
	}
	for _, mf := range metricFamilies {
		if _, err := expfmt.MetricFamilyToText(writer, mf); err != nil {
			return err
		}
	}
	return nil
}

func withDefaults(config Config) Config {
	if config.Namespace == "" {
		config.Namespace = defaultNamespace
	}
	if config.Registerer == nil {
		config.Registerer = prometheus.DefaultRegisterer
	}
	if config.Gatherer == nil {
		if gatherer, ok := config.Registerer.(prometheus.Gatherer); ok {
			config.Gatherer = gatherer
		} else {
			config.Gatherer = prometheus.DefaultGatherer
		}
	}
	if config.CounterOptsFunc == nil {
		config.CounterOptsFunc = func(opts prometheus.CounterOpts) prometheus.CounterOpts { return opts }
	}
	if config.HistogramOptsFunc == nil {
		config.HistogramOptsFunc = func(opts prometheus.HistogramOpts) prometheus.HistogramOpts { return opts }
	}
	if config.timeNow == nil {
		config.timeNow = time.Now
	}
	return config
}

func bucketsOrDefault(provided, fallback []float64) []float64 {
	if len(provided) == 0 {
		return fallback
	}
	return provided
}

func routeLabel(r web.Context, doNotUseRequestPathFor404 bool) string {
	url := r.Path()
	if url == "" && !doNotUseRequestPathFor404 && r.Request() != nil && r.Request().URL != nil {
		url = r.Request().URL.Path
	}
	if url == "" && r.URI() != "" {
		url = r.URI()
	}
	if url == "" {
		return "unknown"
	}
	return url
}

func normalizeStatus(committed bool, status int, path string, err error) int {
	if err != nil && !committed && path == "" {
		return http.StatusNotFound
	}
	if err != nil && !committed {
		return http.StatusInternalServerError
	}
	if status > 0 {
		return status
	}
	if err != nil {
		return http.StatusInternalServerError
	}
	return http.StatusOK
}

func normalizeSize(size int64) int64 {
	if size < 0 {
		return 0
	}
	return size
}

func createLabels(customLabelFuncs map[string]LabelValueFunc) ([]string, []customLabelValuer) {
	labelNames := []string{"code", "method", "host", "url"}
	if len(customLabelFuncs) == 0 {
		return labelNames, nil
	}

	customValuers := make([]customLabelValuer, 0, len(customLabelFuncs))
	for label, labelFunc := range customLabelFuncs {
		customValuers = append(customValuers, customLabelValuer{
			label:     label,
			valueFunc: labelFunc,
		})
	}
	sort.Slice(customValuers, func(i, j int) bool {
		return customValuers[i].label < customValuers[j].label
	})
	for idx, cv := range customValuers {
		labelIndex := containsAt(labelNames, cv.label)
		if labelIndex == -1 {
			labelIndex = len(labelNames)
			labelNames = append(labelNames, cv.label)
		}
		customValuers[idx].index = labelIndex
	}
	return labelNames, customValuers
}

func containsAt[T comparable](haystack []T, needle T) int {
	for i, v := range haystack {
		if v == needle {
			return i
		}
	}
	return -1
}

func computeApproximateRequestSize(r *http.Request) int {
	if r == nil {
		return 0
	}
	size := 0
	if r.URL != nil {
		size += len(r.URL.Path)
	}
	size += len(r.Method)
	size += len(r.Proto)
	for name, values := range r.Header {
		size += len(name)
		for _, value := range values {
			size += len(value)
		}
	}
	size += len(r.Host)
	if r.ContentLength != -1 {
		size += int(r.ContentLength)
	}
	return size
}

func registerCollector[T prometheus.Collector](registerer prometheus.Registerer, collector T) (T, error) {
	if err := registerer.Register(collector); err != nil {
		var zero T
		alreadyRegistered, ok := err.(prometheus.AlreadyRegisteredError)
		if !ok {
			return zero, err
		}
		existing, ok := alreadyRegistered.ExistingCollector.(T)
		if !ok {
			return zero, err
		}
		return existing, nil
	}
	return collector, nil
}
