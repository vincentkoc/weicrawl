#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

allow_missing_live=0
for arg in "$@"; do
  case "$arg" in
    --allow-missing-live)
      allow_missing_live=1
      ;;
    *)
      echo "unknown argument: $arg" >&2
      echo "usage: scripts/release-check.sh [--allow-missing-live]" >&2
      exit 2
      ;;
  esac
done

tag="${WEICRAWL_RELEASE_TAG:-}"
tap_dir="${WEICRAWL_TAP_DIR:-../tap}"

fail() {
  echo "release-check: $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

echo "== release prerequisites =="
need_cmd git
need_cmd go
need_cmd goreleaser
need_cmd python3

if [[ -n "$(git status --porcelain)" ]]; then
  fail "working tree must be clean before release validation"
fi

origin_url="$(git remote get-url origin 2>/dev/null || true)"
if [[ -z "$origin_url" ]]; then
	fail "origin remote is not configured; expected vincentkoc/weicrawl before tagging"
fi
if [[ "$origin_url" != git@github.com:vincentkoc/weicrawl.git &&
	"$origin_url" != https://github.com/vincentkoc/weicrawl.git &&
	"$origin_url" != https://github.com/vincentkoc/weicrawl ]]; then
	fail "origin remote must point at vincentkoc/weicrawl before the local-phase release: $origin_url"
fi

if [[ -z "$tag" ]]; then
  tag="$(git describe --tags --exact-match 2>/dev/null || true)"
fi
if [[ -z "$tag" ]]; then
  fail "set WEICRAWL_RELEASE_TAG=vX.Y.Z or run from an exact release tag"
fi
case "$tag" in
  v[0-9]*.[0-9]*.[0-9]*)
    ;;
  *)
    fail "release tag must be semver-like, got: $tag"
    ;;
esac
if ! git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	if [[ "$allow_missing_live" == "1" ]]; then
		echo "release-check: tag $tag does not exist locally; continuing because this is a non-release dry run"
	else
		fail "tag $tag does not exist locally"
	fi
fi

if [[ ! -d "$tap_dir" ]]; then
  fail "tap repo not found at $tap_dir; set WEICRAWL_TAP_DIR"
fi
if [[ ! -f "$tap_dir/.github/workflows/update-formula.yml" || ! -x "$tap_dir/.github/scripts/update_formula.py" ]]; then
  fail "tap repo at $tap_dir is missing the generic formula updater"
fi

echo "== local e2e =="
./scripts/e2e-local.sh

echo "== live copied-snapshot proof =="
if [[ -z "${WEICRAWL_LIVE_KEYS:-}" ]]; then
  if [[ "$allow_missing_live" == "1" ]]; then
    echo "release-check: skipping live unlock proof because WEICRAWL_LIVE_KEYS is unset"
  else
    fail "WEICRAWL_LIVE_KEYS is required for release readiness; use --allow-missing-live only for non-release dry runs"
  fi
else
  workdir="$(mktemp -d)"
  cleanup() {
    rm -rf "$workdir"
  }
  trap cleanup EXIT
  WEICRAWL_LIVE_WORKDIR="$workdir" \
    WEICRAWL_LIVE_UNLOCK_SYNC=1 \
    ./scripts/live-copy-snapshot.sh > "$workdir/live-copy.json"
  python3 - "$workdir/live-copy.json" <<'PY'
import json
import pathlib
import sys

payload = json.load(open(sys.argv[1]))
if not payload.get("manifest_valid"):
    raise SystemExit(f"live key manifest did not validate: {payload}")
snapshot = payload.get("snapshot_path")
if not snapshot or not pathlib.Path(snapshot).is_dir():
    raise SystemExit(f"live snapshot missing: {payload}")
print(json.dumps({
    "status": payload.get("status"),
    "source_db_count": payload.get("source_db_count"),
    "encrypted_db_count": payload.get("encrypted_db_count"),
    "manifest_valid": payload.get("manifest_valid"),
}, indent=2))
PY
fi

echo "== release build =="
goreleaser check --config .goreleaser.yaml
goreleaser release --snapshot --clean --skip=publish --config .goreleaser.yaml

echo "release-check ok for $tag"
