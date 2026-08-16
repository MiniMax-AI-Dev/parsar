#!/usr/bin/env bash
# Thin shim over scripts/dev-env.sh:parsar_dev_start_db. Kept for
# back-compat with external scripts and the CI `check-store` job path.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/dev-env.sh
source "$ROOT_DIR/scripts/dev-env.sh"

ROOT_DIR="$ROOT_DIR" parsar_dev_start_db
