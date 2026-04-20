package main

import (
	"context"
	"fmt"
	"github.com/goforj/web/webindex"
)

func main() {
	manifest, err := webindex.Run(context.Background(), webindex.IndexOptions{
		Root:    ".",
		OutPath: "webindex.json",
	})
	fmt.Println(err == nil, manifest.Version != "")
	// true true
}
