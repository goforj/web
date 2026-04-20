package main

import (
	"bytes"
	"fmt"
	"github.com/goforj/web"
	"github.com/goforj/web/webprometheus"
	"github.com/goforj/web/webtest"
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"net/http/httptest"
	"strings"
)

func main() {
	registry := prometheus.NewRegistry()
	metrics, _ := webprometheus.New(webprometheus.Config{Registerer: registry, Gatherer: registry, Namespace: "example"})
	handler := metrics.Middleware()(func(c web.Context) error { return c.NoContent(http.StatusNoContent) })
	ctx := webtest.NewContext(httptest.NewRequest(http.MethodGet, "/healthz", nil), nil, "/healthz", nil)
	_ = handler(ctx)
	out := &bytes.Buffer{}
	_ = webprometheus.WriteGatheredMetrics(out, registry)
	fmt.Println(strings.Contains(out.String(), "example_requests_total"))
	// true
}
