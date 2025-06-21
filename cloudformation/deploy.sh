#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(dirname "${BASH_SOURCE[0]}")
cd "$SCRIPT_DIR"

./00-deploy-s3-bucket.sh
./10-deploy-postgres-lab.sh
