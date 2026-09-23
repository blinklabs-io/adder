#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

if [[ "${1:-}" == "--help" ]]; then
    cat <<'EOF'
Usage: scripts/test-integration.sh [go test flags]

Builds a race-enabled Adder CLI and tests it against a local UTxO RPC fixture.
Requires Bash, Go (see go.mod), and a C compiler for the race detector.
No Cardano node or credentials are needed. Test logs are retained in a new
temporary directory printed at startup. Examples:

  scripts/test-integration.sh
  scripts/test-integration.sh -run TestOutputLevelIntegration -count=3
  scripts/test-integration.sh -run TestConfigurationIntegration
  scripts/test-integration.sh -run TestFilteringIntegration
  scripts/test-integration.sh -run TestStartupIntegration/api-port-occupied
  scripts/test-integration.sh -run TestReconnectIntegration
EOF
    exit 0
fi

run_dir="$(mktemp -d "${TMPDIR:-/tmp}/adder-integration.XXXXXX")"
printf 'Integration artifacts: %s\n' "$run_dir"
go build -race -o "$run_dir/adder" ./cmd/adder
export ADDER_INTEGRATION_BINARY="$run_dir/adder"
export ADDER_INTEGRATION_ARTIFACTS="$run_dir"
go test -race -tags localintegration ./tests/integration -v -count=1 -timeout=5m "$@" \
    2>&1 | tee "$run_dir/tests.log"
