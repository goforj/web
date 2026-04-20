package main

import (
	"fmt"
	"github.com/goforj/web/webprometheus"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	metrics := webprometheus.MustNew(webprometheus.Config{Registerer: prometheus.NewRegistry(), Gatherer: prometheus.NewRegistry()})
	fmt.Println(metrics != nil)
	// true
}
