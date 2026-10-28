#!/usr/bin/env bash
# Builds the binary and runs every suite against a fresh database + mock Frappe (each suite isolated).
# Requires: go, node, and Playwright (for the ui-* suites).
set -u
cd "$(dirname "$0")/.."
go vet ./... && go build -o rapid-entry . || exit 1
rc=0
for suite in "node tests/acceptance.mjs" "node tests/ui.cjs" "node tests/ui-admin.cjs" "node tests/ui-correction.cjs"; do
  pkill -x rapid-entry 2>/dev/null; sleep 0.5
  rm -rf /tmp/rea-test && DB_PATH=/tmp/rea-test/rapid.db DEV_OTP=1 SEED_DEMO=1 ADMIN_MOBILE=9000000000 MOCK_FRAPPE_ADDR=:8099 ./rapid-entry >/tmp/rea-test.log 2>&1 &
  for _ in $(seq 1 50); do curl -sf localhost:8080/healthz >/dev/null && break; sleep 0.1; done
  echo "=== $suite"
  $suite || rc=1
done
pkill -x rapid-entry 2>/dev/null
exit $rc
