.PHONY: bench bench-frameworks benchmark-svg benchmark-svg-preview benchmark-svg-render benchmark-svg-verify test

bench:
	GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache go test ./adapter/echoweb -run '^$$' -bench 'Benchmark(Echo|Web)(PlainText|ParamsJSON|MiddlewareChain|MiddlewareChainSingleUse|GroupAndRouteMiddleware|Compress|BodyDump|WebSocketJSON|WebSocketJSONPersistent)$$' -benchmem

bench-frameworks:
	cd docs && GOWORK=off GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache go test ./bench -run '^$$' -bench '^BenchmarkHTTPStacks$$' -benchmem -count=1 -cpu=1

benchmark-svg:
	cd docs && BENCH_RENDER=1 BENCH_REQUIRE_CLEAN_SNAPSHOT=1 GOWORK=off GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache go test -tags=benchrender ./bench -run '^TestRenderBenchmarks$$' -count=1 -v

benchmark-svg-preview:
	cd docs && BENCH_RENDER=1 BENCH_ALLOW_DIRTY=1 GOWORK=off GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache go test -tags=benchrender ./bench -run '^TestRenderBenchmarks$$' -count=1 -v

benchmark-svg-render:
	cd docs && BENCH_RENDER=1 BENCH_RENDER_ONLY=1 GOWORK=off GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache go test -tags=benchrender ./bench -run '^TestRenderBenchmarks$$' -count=1 -v

benchmark-svg-verify:
	cd docs && BENCH_RENDER=1 BENCH_RENDER_ONLY=1 BENCH_REQUIRE_CLEAN_SNAPSHOT=1 GOWORK=off GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache go test -tags=benchrender ./bench -run '^TestRenderBenchmarks$$' -count=1 -v

test:
	GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache go test ./...
