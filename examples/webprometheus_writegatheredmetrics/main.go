package main

import (
	"bytes"
	"fmt"
	"github.com/goforj/web/webprometheus"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	var buf bytes.Buffer
	err := webprometheus.WriteGatheredMetrics(&buf, prometheus.NewRegistry())
	fmt.Println(err == nil)
	// true
}
