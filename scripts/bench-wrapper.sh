#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export GOCACHE="${GOCACHE:-/tmp/gocache}"
export GOMODCACHE="${GOMODCACHE:-/tmp/gomodcache}"

MODE="${1:-representative}"
PROFILE="${PROFILE:-quick}"
CPU="${CPU:-}"
KEEP_BENCH_FILES="${KEEP_BENCH_FILES:-0}"

case "$PROFILE" in
  quick)
    BENCHTIME="${BENCHTIME:-2s}"
    COUNT="${COUNT:-2}"
    ;;
  stable)
    BENCHTIME="${BENCHTIME:-5s}"
    COUNT="${COUNT:-4}"
    CPU="${CPU:-1}"
    ;;
  *)
    echo "invalid PROFILE: $PROFILE (expected quick or stable)" >&2
    exit 1
    ;;
esac

if [[ -z "$CPU" ]]; then
  if command -v nproc >/dev/null 2>&1; then
    CPU="$(nproc)"
  else
    CPU="$(getconf _NPROCESSORS_ONLN)"
  fi
fi

run_bench() {
  local label="$1"
  local pattern="$2"
  echo
  echo "== $label =="
  go test ./adapter/echoweb \
    -run '^$' \
    -bench "$pattern" \
    -benchmem \
    -benchtime "$BENCHTIME" \
    -count "$COUNT" \
    -cpu "$CPU"
}

run_bench_to_file() {
  local label="$1"
  local pattern="$2"
  local outfile="$3"
  echo
  echo "== $label =="
  echo "writing benchmark output to $outfile"
  go test ./adapter/echoweb \
    -run '^$' \
    -bench "$pattern" \
    -benchmem \
    -benchtime "$BENCHTIME" \
    -count "$COUNT" \
    -cpu "$CPU" | tee "$outfile"
}

run_benchstat() {
  local before="$1"
  local after="$2"
  local normalized_before="$before.normalized"
  local normalized_after="$after.normalized"

  sed -E 's/BenchmarkEcho/Benchmark/g' "$before" >"$normalized_before"
  sed -E 's/BenchmarkWeb/Benchmark/g' "$after" >"$normalized_after"

  echo
  echo "== benchstat =="
  if command -v benchstat >/dev/null 2>&1; then
    benchstat "$normalized_before" "$normalized_after"
    return
  fi

  go run golang.org/x/perf/cmd/benchstat@latest "$normalized_before" "$normalized_after"
}

with_comparison() {
  local echo_label="$1"
  local echo_pattern="$2"
  local web_label="$3"
  local web_pattern="$4"
  local prefix
  prefix="$(mktemp -d)"
  local echo_file="$prefix/echo.txt"
  local web_file="$prefix/web.txt"

  run_bench_to_file "$echo_label" "$echo_pattern" "$echo_file"
  run_bench_to_file "$web_label" "$web_pattern" "$web_file"
  run_benchstat "$echo_file" "$web_file"

  if [[ "$KEEP_BENCH_FILES" == "1" ]]; then
    echo
    echo "kept benchmark outputs in $prefix"
  else
    rm -rf "$prefix"
  fi
}

case "$MODE" in
  representative)
    echo "Running representative benchmarks in isolated Echo and web processes..."
    echo "PROFILE=$PROFILE BENCHTIME=$BENCHTIME COUNT=$COUNT CPU=$CPU"
    if [[ "$PROFILE" == "stable" ]]; then
      with_comparison \
        "Echo representative" '^BenchmarkEchoRepresentative/(plain_text|params_json|middleware_chain)$' \
        "web representative" '^BenchmarkWebRepresentative/(plain_text|params_json|middleware_chain)$'
    else
      run_bench "Echo representative" '^BenchmarkEchoRepresentative/(plain_text|params_json|middleware_chain)$'
      run_bench "web representative" '^BenchmarkWebRepresentative/(plain_text|params_json|middleware_chain)$'
    fi
    ;;
  bare)
    echo "Running bare handler benchmarks in isolated Echo and web processes..."
    echo "PROFILE=$PROFILE BENCHTIME=$BENCHTIME COUNT=$COUNT CPU=$CPU"
    if [[ "$PROFILE" == "stable" ]]; then
      with_comparison \
        "Echo bare" '^BenchmarkEchoBareHandler' \
        "web bare" '^BenchmarkWebBareHandler'
    else
      run_bench "Echo bare" '^BenchmarkEchoBareHandler'
      run_bench "web bare" '^BenchmarkWebBareHandler'
    fi
    ;;
  all)
    echo "Running bare and representative benchmarks in isolated Echo and web processes..."
    echo "PROFILE=$PROFILE BENCHTIME=$BENCHTIME COUNT=$COUNT CPU=$CPU"
    if [[ "$PROFILE" == "stable" ]]; then
      with_comparison \
        "Echo bare" '^BenchmarkEchoBareHandler' \
        "web bare" '^BenchmarkWebBareHandler'
      with_comparison \
        "Echo representative" '^BenchmarkEchoRepresentative' \
        "web representative" '^BenchmarkWebRepresentative'
    else
      run_bench "Echo bare" '^BenchmarkEchoBareHandler'
      run_bench "web bare" '^BenchmarkWebBareHandler'
      run_bench "Echo representative" '^BenchmarkEchoRepresentative'
      run_bench "web representative" '^BenchmarkWebRepresentative'
    fi
    ;;
  *)
    echo "usage: scripts/bench-wrapper.sh [representative|bare|all]" >&2
    echo "optional env: PROFILE=quick|stable BENCHTIME=2s COUNT=2 CPU=<n> KEEP_BENCH_FILES=1" >&2
    exit 1
    ;;
esac
