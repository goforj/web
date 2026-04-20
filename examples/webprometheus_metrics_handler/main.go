package main

import (
	"fmt"
	"github.com/goforj/web/webprometheus"
	"github.com/goforj/web/webtest"
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"net/http/httptest"
	"strings"
)

func main() {
	registry := prometheus.NewRegistry()
	counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "demo_total", Help: "demo counter"})
	registry.MustRegister(counter)
	counter.Inc()
	metrics, _ := webprometheus.New(webprometheus.Config{Registerer: prometheus.NewRegistry(), Gatherer: registry})
	recorder := httptest.NewRecorder()
	ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/metrics", nil), recorder, "/metrics", nil)
	_ = metrics.Handler()(ctx)
	fmt.Println(strings.Contains(recorder.Body.String(), "demo_total"))
	// true
}
