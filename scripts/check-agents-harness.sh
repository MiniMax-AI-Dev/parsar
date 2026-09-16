#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
package="$repo_root/packages/codex-harness"
export PYTHONDONTWRITEBYTECODE=1
python3 "$package/prepare.py" --check
python3 "$package/prepare_test.py"
bash -n "$repo_root/scripts/build-agents-harness.sh" "$repo_root/scripts/check-agents-harness.sh"
