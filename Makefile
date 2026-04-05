bench:
	GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache go test ./adapter/echoweb -run '^$$' -bench 'Benchmark(Echo|Web)(PlainText|ParamsJSON|MiddlewareChain|MiddlewareChainSingleUse|GroupAndRouteMiddleware|Compress|BodyDump|WebSocketJSON|WebSocketJSONPersistent)$$' -benchmem

test:
	GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache go test ./...
