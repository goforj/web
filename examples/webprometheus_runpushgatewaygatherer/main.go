package main

import (
	"context"
	"fmt"
	"github.com/goforj/web/webprometheus"
)

func main() {
	err := webprometheus.RunPushGatewayGatherer(context.Background(), webprometheus.PushGatewayConfig{})
	fmt.Println(err != nil)
	// true
}
