#!/bin/sh
set -eu

die() {
  printf '%s\n' "$*" >&2
  exit 1
}

helper_root="${WEICRAWL_WECHAT_KEY_HELPER_ROOT:-${1:-}}"
if [ "$helper_root" = "--check" ]; then
  helper_root="${WEICRAWL_WECHAT_KEY_HELPER_ROOT:-}"
  check_only=1
else
  check_only=0
fi
if [ -z "$helper_root" ] && [ -f "./find_key_memscan.py" ]; then
  helper_root="$PWD"
fi

lldb_bin="${WEICRAWL_LLDB:-/usr/bin/lldb}"
python_bin="${WEICRAWL_PYTHON:-/usr/bin/python3}"

command -v "$lldb_bin" >/dev/null 2>&1 || die "lldb not found at $lldb_bin"
command -v "$python_bin" >/dev/null 2>&1 || die "python not found at $python_bin"

lldb_python="$("$lldb_bin" -P)"
PYTHONNOUSERSITE=1 PYTHONPATH="$lldb_python" "$python_bin" - <<'PY' >/dev/null
import lldb
PY

devtools_status="$(DevToolsSecurity -status 2>/dev/null || true)"
if printf '%s\n' "$devtools_status" | grep -qi 'disabled'; then
  die "Developer Tools security is disabled. Run: sudo DevToolsSecurity -enable"
fi

if [ "$check_only" = "1" ]; then
  printf 'no-sip scanner prerequisites look usable\n'
  exit 0
fi

[ -n "${WEICRAWL_SCAN_OUT:-}" ] || die "WEICRAWL_SCAN_OUT is required"
[ -n "$helper_root" ] || die "helper root is required; pass /path/to/wechat-db-decrypt-macos or set WEICRAWL_WECHAT_KEY_HELPER_ROOT"
[ -f "$helper_root/find_key_memscan.py" ] || die "find_key_memscan.py not found under $helper_root"

rm -f "$helper_root/wechat_keys.json" "$WEICRAWL_SCAN_OUT"
tmp_output="$(mktemp)"
trap 'rm -f "$tmp_output"' EXIT

set +e
(
  cd "$helper_root"
  PYTHONNOUSERSITE=1 PYTHONPATH="$lldb_python" "$python_bin" ./find_key_memscan.py
) >"$tmp_output" 2>&1
code=$?
set -e

cat "$tmp_output"

if [ "$code" -ne 0 ]; then
  if grep -qi 'Not allowed to attach\\|cannot get permission to debug' "$tmp_output"; then
    printf '%s\n' "Attach was denied without SIP changes." >&2
    printf '%s\n' "Try from an interactive Terminal after: sudo DevToolsSecurity -enable" >&2
    printf '%s\n' "If it still fails, this helper cannot extract keys without modifying the target app signature." >&2
  fi
  exit "$code"
fi

if [ ! -f "$helper_root/wechat_keys.json" ]; then
  die "scanner completed but did not write $helper_root/wechat_keys.json"
fi

out_dir="$(dirname "$WEICRAWL_SCAN_OUT")"
mkdir -p "$out_dir"
cp "$helper_root/wechat_keys.json" "$WEICRAWL_SCAN_OUT"
chmod 600 "$WEICRAWL_SCAN_OUT"
printf 'copied scanner manifest to %s\n' "$WEICRAWL_SCAN_OUT"
