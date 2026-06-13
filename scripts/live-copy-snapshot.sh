#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

real_home="${HOME:?HOME is required for macOS WeChat discovery}"
workdir="${WEICRAWL_LIVE_WORKDIR:-$(mktemp -d)}"
mkdir -p "$workdir/bin" "$workdir/config" "$workdir/cache"

GOBIN="$workdir/bin" GOWORK=off go install ./cmd/weicrawl
weicrawl="$workdir/bin/weicrawl"

export HOME="$real_home"
export XDG_CONFIG_HOME="$workdir/config"
export WEICRAWL_DB_PATH="${WEICRAWL_LIVE_DB_PATH:-$workdir/weicrawl-live.db}"
export WEICRAWL_CACHE_DIR="${WEICRAWL_LIVE_CACHE_DIR:-$workdir/cache/weicrawl}"

"$weicrawl" --json doctor > "$workdir/doctor.json"
if [[ -n "${WEICRAWL_LIVE_PROFILE:-}" ]]; then
  "$weicrawl" --json sync --source desktop-macos --keep-source-snapshot --profile "$WEICRAWL_LIVE_PROFILE" > "$workdir/sync.json"
else
  "$weicrawl" --json sync --source desktop-macos --keep-source-snapshot > "$workdir/sync.json"
fi

snapshot_path="$(python3 - "$workdir/sync.json" <<'PY'
import json
import sys
print(json.load(open(sys.argv[1])).get("snapshot_path", ""))
PY
)"
if [[ -n "$snapshot_path" ]]; then
  "$weicrawl" --json unlock template --snapshot "$snapshot_path" --out "$workdir/wechat_keys.template.json" > "$workdir/unlock-template.json"
  if [[ -n "${WEICRAWL_LIVE_KEYS:-}" ]]; then
    "$weicrawl" --json unlock validate --snapshot "$snapshot_path" --keys "$WEICRAWL_LIVE_KEYS" > "$workdir/unlock-validate.json"
  fi
fi

python3 - "$workdir" <<'PY'
import json
import os
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
doctor = json.loads((root / "doctor.json").read_text())
sync = json.loads((root / "sync.json").read_text())
template = json.loads((root / "unlock-template.json").read_text()) if (root / "unlock-template.json").exists() else {}
validate = json.loads((root / "unlock-validate.json").read_text()) if (root / "unlock-validate.json").exists() else {}

snapshot = sync.get("snapshot_path") or ""
if not snapshot:
    raise SystemExit(f"sync did not produce snapshot_path: {sync}")
if not pathlib.Path(snapshot).is_dir():
    raise SystemExit(f"snapshot_path does not exist: {snapshot}")

desktop = doctor.get("desktop_macos", {})
summary = {
    "workdir": str(root),
    "db_path": os.environ.get("WEICRAWL_DB_PATH", ""),
    "cache_dir": os.environ.get("WEICRAWL_CACHE_DIR", ""),
    "profile": sync.get("profile_id"),
    "status": sync.get("status"),
    "snapshot_path": snapshot,
    "manifest_template_path": template.get("manifest_path"),
    "manifest_template_db_count": template.get("db_count"),
    "manifest_valid": validate.get("available"),
    "source_db_count": sync.get("source_db_count"),
    "snapshot_key_info_db_count": sync.get("key_info_db_count"),
    "encrypted_db_count": desktop.get("encrypted_db_count"),
    "key_info_db_count": desktop.get("key_info_db_count"),
    "imported_messages": sync.get("imported_messages"),
    "warnings": sync.get("warnings", []),
}
print(json.dumps(summary, indent=2))
PY

snapshot_path="$(python3 - "$workdir/sync.json" <<'PY'
import json
import sys
print(json.load(open(sys.argv[1])).get("snapshot_path", ""))
PY
)"

if [[ -n "${WEICRAWL_LIVE_KEYS:-}" ]]; then
  WEICRAWL_LIVE_SNAPSHOT="$snapshot_path" ./scripts/e2e-local.sh
fi
