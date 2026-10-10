#!/bin/sh
set -eu
AUTH_DEMO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
python3 "$AUTH_DEMO_DIR/demo.py" demo --pause
