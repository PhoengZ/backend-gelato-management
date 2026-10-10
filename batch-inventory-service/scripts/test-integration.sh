#!/bin/sh
set -eu
: "${TEST_DATABASE_URL:?Set an isolated PostgreSQL URL}"
: "${TEST_RABBITMQ_URL:?Set an isolated RabbitMQ URL}"
: "${TEST_REDIS_URL:?Set an isolated Redis URL}"
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
run_dir=$(mktemp -d)
catalog_pid=
cleanup() {
  if [ -n "$catalog_pid" ]; then kill -TERM "$catalog_pid" 2>/dev/null || true; wait "$catalog_pid" 2>/dev/null || true; fi
  rm -rf "$run_dir"
}
trap cleanup EXIT HUP INT TERM
export JWT_SECRET=inventory_test_jwt_secret_not_for_production_123
export JWT_ISSUER=gelatoflow-auth JWT_AUDIENCE=gelatoflow-api
export TEST_CATALOG_REST_URL=http://127.0.0.1:54800 TEST_CATALOG_GRPC_ADDR=127.0.0.1:54801
python3 - <<'PY'
import socket
for port in (54800, 54801):
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', port))
PY
(cd "$repo_root/catalog-service" && go build -o "$run_dir/catalog" ./cmd/api)
PORT=54800 GRPC_ADDR=127.0.0.1:54801 REDIS_URL="$TEST_REDIS_URL" REDIS_KEY_PREFIX="inventory:tests:$$" "$run_dir/catalog" > "$run_dir/catalog.log" 2>&1 &
catalog_pid=$!
attempt=0
until curl --fail --silent "$TEST_CATALOG_REST_URL/health" >/dev/null; do
  if ! kill -0 "$catalog_pid" 2>/dev/null; then cat "$run_dir/catalog.log"; exit 1; fi
  attempt=$((attempt + 1)); if [ "$attempt" -ge 50 ]; then cat "$run_dir/catalog.log"; exit 1; fi
  sleep 0.1
done
cd "$repo_root/batch-inventory-service"
INTEGRATION_REQUIRED=1 go test -race -count=1 -coverpkg=./... -coverprofile=coverage.out ./...
