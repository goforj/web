# HTTP Stack Benchmark Methodology

This suite produces the checked-in framework comparison shown in the project README. The measured snapshot in `benchmarks_rows.json` is the source of truth; `framework_comparison.svg` is a deterministic rendering of that snapshot.

Snapshot schema 3 intentionally requires a fresh measurement when upgrading from earlier snapshots because prior schemas did not record the benchmark-input fingerprint, kernel, or material Go build settings.

## Compared Stacks

The suite compares GoForj Web with the `net/http` standard library, Echo, Gin, Chi, Gorilla Mux, and httprouter. Every entry implements `http.Handler`, so the same requests, semantic endpoint contract, and Go benchmark harness exercise each stack through its idiomatic APIs.

Echo is pinned to the version used by GoForj Web. This keeps the direct Echo row useful for measuring the cost of GoForj Web's application-facing abstraction instead of mixing in a runtime-version change.

Fiber is not included because its native server is based on `fasthttp`. Running Fiber through a `net/http` adapter would measure conversion overhead, while placing native Fiber dispatch beside `ServeHTTP` would compare different server contracts. A separate native-server benchmark would be required for a defensible Fiber comparison.

## Access Patterns

- `live_plain_text` sends `GET /benchmark/live` over a real HTTP/1.1 loopback connection with keep-alive enabled and returns `Hello, benchmark!`. Its result is reported as loopback requests per second.
- `static_text` dispatches `GET /benchmark/static` in process and writes a UTF-8 `text/plain` response containing `Hello, benchmark!`.
- `path_param_json` dispatches `GET /benchmark/users/42`, reads the route parameter, and emits the same small payload through each stack's idiomatic JSON API. The contract test decodes the JSON semantically because framework defaults can differ in insignificant whitespace and content-type parameters.
- `middleware_chain` dispatches the exact same `GET /benchmark/static` route and plaintext response as `static_text`, but adds three native middleware stages: store request-scoped state, set a response header, then read and validate the state. The dashboard reports both total throughput and absolute latency added above that identical route baseline.

The in-process cases reuse a request and resettable response writer so the chart does not primarily measure request construction or `httptest.ResponseRecorder` allocation. Correctness checks run outside the timed loop and as ordinary unit tests. Allocations describe the complete in-process route and handler contract; live-loopback allocations include the HTTP client and server and are therefore not presented as framework allocations.

## Regeneration

Run a fresh measurement and regenerate the snapshot, SVG, and README block:

```sh
make bench-svg
```

Publication measurements must start from a clean working tree. For local iteration on uncommitted code, generate an explicitly marked, non-publishable preview instead:

```sh
make bench-svg-preview
```

Regenerate only the deterministic documentation output from the checked-in snapshot:

```sh
make bench-svg-render
```

For benchmark artifacts, CI runs only the render-only publication check. It verifies that the SVG and README match the committed snapshot and rejects snapshots recorded from a dirty tree. It also recomputes a deterministic fingerprint over the local runtime sources compiled into the handlers, the benchmark harness, and the root and docs module pins. A generated-artifact commit therefore remains valid, while a later benchmark input change requires a new measurement. This is source-currentness validation; CI does not execute the benchmarks, compare different machines, or enforce performance thresholds.

The measurement command fixes `GOMAXPROCS` to one and records the Go version, operating system, architecture, CPU, kernel, module versions, sample count, benchmark duration, repository revision, working-tree state, source fingerprint, and material Go build settings (`GOFLAGS`, `GOEXPERIMENT`, `GODEBUG`, `CGO_ENABLED`, and the active architecture tuning variable). The default seven samples each run in a separate benchmark process. Their framework order rotates so every stack occupies every run-order position exactly once, distributing startup, CPU-frequency, and thermal effects instead of consistently favoring one row. The loopback case therefore measures warm, single-connection HTTP/1.1 throughput rather than maximum concurrent server capacity.

Set `BENCH_COUNT` to a multiple of seven for a longer publication run, such as 14. Publication requires a time-based `BENCHTIME` of at least `1s` and rejects incomplete rotations. Local preview runs may use a smaller count or shorter benchmark time when turnaround matters more than balanced results.

Each displayed value is the median of the recorded samples, and bars are scaled independently within each panel. Whiskers in every panel show the observed sample minimum and maximum. Middleware added latency is paired by process: the renderer subtracts a stack's plaintext result from its middleware result for each sample, then displays the median of those differences. Use the raw rows to inspect the complete spread; small differences are not rankings, and absolute numbers from different hardware or Go versions are not directly comparable.

These are focused microbenchmarks and loopback ceilings, not production capacity forecasts. Application code, payloads, middleware, TLS, proxies, databases, scheduling, and deployment topology usually dominate end-to-end performance.
