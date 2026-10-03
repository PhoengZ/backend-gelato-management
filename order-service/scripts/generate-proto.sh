#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"
test "$(buf --version)" = "1.73.0" || { echo 'Order generation requires Buf 1.73.0' >&2; exit 1; }
buf generate contracts \
  --path contracts/proto/inventory/v1/inventory.proto \
  --path contracts/proto/catalog/v1/catalog.proto \
  --template order-service/buf.gen.yaml
