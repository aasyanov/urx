#!/usr/bin/env bash
# Full quality run: vet, golangci-lint, tests (race+cover), benchmarks, fuzz.
# All output goes to quality.result in the repo root.

set -uo pipefail

FUZZ_TIME="${FUZZ_TIME:-30s}"
BENCH_COUNT="${BENCH_COUNT:-3}"
OUT_FILE="${OUT_FILE:-quality.result}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"
OUT_PATH="$ROOT/$OUT_FILE"

started_epoch=$(date +%s)
started_human=$(date '+%Y-%m-%d %H:%M:%S')
failures=()
current_title=""

section_header() {
    local title="$1"
    current_title="$title"
    {
        echo
        printf '=%.0s' {1..72}; echo
        echo "$title"
        echo "Started: $(date '+%Y-%m-%d %H:%M:%S')"
        printf '=%.0s' {1..72}; echo
        echo
    } | tee -a "$OUT_PATH"
}

section_footer() {
    local exit_code="$1"
    {
        echo
        echo "Exit code: $exit_code"
        echo "Finished: $(date '+%Y-%m-%d %H:%M:%S')"
        echo
    } | tee -a "$OUT_PATH"
    if [[ "$exit_code" -ne 0 ]]; then
        failures+=("$current_title")
    fi
}

run_section() {
    local title="$1"
    shift
    section_header "$title"
    "$@" 2>&1 | tee -a "$OUT_PATH"
    local exit_code=${PIPESTATUS[0]}
    section_footer "$exit_code"
    return "$exit_code"
}

{
    echo "urx quality run"
    echo "Started: $started_human"
    echo "Root:    $ROOT"
    echo "Go:      $(go version 2>&1)"
    echo
} > "$OUT_PATH"

run_section "go vet ./..." go vet ./...

run_section "golangci-lint run ./..." golangci-lint run ./...

run_section "go test -race -count=1 -timeout=120s -coverprofile=coverage.txt ./..." \
    go test -race -count=1 -timeout=120s -coverprofile=coverage.txt ./...

run_section "go tool cover -func=coverage.txt" \
    go tool cover -func=coverage.txt

export GOMAXPROCS="${GOMAXPROCS:-$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 1)}"
run_section "go test -bench=Benchmark -benchmem -count=${BENCH_COUNT} -run='^$' -timeout=30m ./..." \
    go test -bench=Benchmark -benchmem -count="$BENCH_COUNT" -run='^$' -timeout=30m ./...

while IFS= read -r file; do
    dir=$(dirname "$file")
    while IFS= read -r func; do
        [[ -z "$func" ]] && continue
        run_section "go test -fuzz=^${func}$ -fuzztime=${FUZZ_TIME} ${dir}" \
            go test -fuzz="^${func}$" -fuzztime="$FUZZ_TIME" "$dir"
    done < <(grep '^func Fuzz' "$file" | sed -n 's/^func \(Fuzz[^ (]*\).*/\1/p')
done < <(grep -rl '^func Fuzz' --include='*_test.go' . | grep -v '/vendor/' | sort)

finished_human=$(date '+%Y-%m-%d %H:%M:%S')
finished_epoch=$(date +%s)
duration=$((finished_epoch - started_epoch))
hours=$((duration / 3600))
minutes=$(((duration % 3600) / 60))
seconds=$((duration % 60))

{
    echo
    printf '=%.0s' {1..72}; echo
    echo "SUMMARY"
    printf '=%.0s' {1..72}; echo
    echo "Started:  $started_human"
    echo "Finished: $finished_human"
    printf 'Duration: %02d:%02d:%02d\n' "$hours" "$minutes" "$seconds"
    echo "Output:   $OUT_PATH"
    if ((${#failures[@]} == 0)); then
        echo "Result:   ALL PASSED"
    else
        echo "Result:   FAILED (${#failures[@]} section(s))"
        echo "Failed sections:"
        for f in "${failures[@]}"; do
            echo "  - $f"
        done
    fi
    echo
} | tee -a "$OUT_PATH"

if ((${#failures[@]} > 0)); then
    exit 1
fi
