#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"
if [ "$(buf --version)" != "1.73.0" ]; then
  echo 'Catalog generation requires Buf 1.73.0' >&2
  exit 1
fi
buf generate contracts --path contracts/proto/catalog/v1/catalog.proto \
  --template catalog-service/buf.gen.yaml
