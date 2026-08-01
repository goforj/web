GREEN  := $(shell tput -Txterm setaf 2)
WHITE  := $(shell tput -Txterm setaf 7)
YELLOW := $(shell tput -Txterm setaf 3)
RESET  := $(shell tput -Txterm sgr0)

.PHONY: help

HELP_FUN = %help; while(<>) { if (/^([A-Za-z0-9_-]+)\s*:.*\#\#(?:@([A-Za-z0-9_-]+))?\s(.*)$$/) { push @{$$help{$$2 || "other"}}, [$$1, $$3]; $$width = length($$1) if length($$1) > $$width } } print "\n"; for $$category (sort keys %help) { print "${WHITE}$$category${RESET}\n"; for $$entry (@{$$help{$$category}}) { printf "  ${YELLOW}%-*s${RESET}  ${GREEN}%s${RESET}\n", $$width, $$entry->[0], $$entry->[1] } }

help: ##@other Show this help.
	@perl -e '$(HELP_FUN)' $(MAKEFILE_LIST)

##@benchmark
bench: ##@benchmark Benchmark the Echo adapter.
	go test ./adapter/echoweb -run '^$$' -bench 'Benchmark(Echo|Web)(PlainText|ParamsJSON|MiddlewareChain|MiddlewareChainSingleUse|GroupAndRouteMiddleware|Compress|BodyDump|WebSocketJSON|WebSocketJSONPersistent)$$' -benchmem

bench-frameworks: ##@benchmark Benchmark supported HTTP frameworks.
	cd docs && GOWORK=off go test ./bench -run '^$$' -bench '^BenchmarkHTTPStacks$$' -benchmem -count=1 -cpu=1

benchmark-svg: ##@benchmark Render and verify benchmark SVG snapshots.
	cd docs && BENCH_RENDER=1 BENCH_REQUIRE_CLEAN_SNAPSHOT=1 GOWORK=off go test -tags=benchrender ./bench -run '^TestRenderBenchmarks$$' -count=1 -v

benchmark-svg-preview: ##@benchmark Render benchmark SVG previews without a clean snapshot.
	cd docs && BENCH_RENDER=1 BENCH_ALLOW_DIRTY=1 GOWORK=off go test -tags=benchrender ./bench -run '^TestRenderBenchmarks$$' -count=1 -v

benchmark-svg-render: ##@benchmark Render benchmark SVGs without verification.
	cd docs && BENCH_RENDER=1 BENCH_RENDER_ONLY=1 GOWORK=off go test -tags=benchrender ./bench -run '^TestRenderBenchmarks$$' -count=1 -v

benchmark-svg-verify: ##@benchmark Verify benchmark SVG snapshots.
	cd docs && BENCH_RENDER=1 BENCH_RENDER_ONLY=1 BENCH_REQUIRE_CLEAN_SNAPSHOT=1 GOWORK=off go test -tags=benchrender ./bench -run '^TestRenderBenchmarks$$' -count=1 -v

##@quality
test: ##@quality Run the test suites.
	go test ./...
	go -C docs test ./...
	go -C examples test ./...

vet: ##@quality Run Go vet for every module.
	go vet ./...
	go -C docs vet ./...
	go -C examples vet ./...

##@documentation
generate: ##@documentation Regenerate documentation examples and README content.
	go -C docs run ./examplegen/main.go
	go -C docs run ./readme/main.go
