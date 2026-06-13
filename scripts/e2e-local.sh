#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

echo "== go gate =="
GOWORK=off go mod tidy
git diff --exit-code -- go.mod go.sum
GOWORK=off go vet ./...
GOWORK=off go test -count=1 ./...
git diff --check

echo "== live-safe cli smoke =="
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT

export HOME="$tmpdir/home"
export XDG_CONFIG_HOME="$tmpdir/config"
export XDG_CACHE_HOME="$tmpdir/cache"
export WEICRAWL_DB_PATH="$tmpdir/weicrawl.db"
export WEICRAWL_CACHE_DIR="$tmpdir/cache/weicrawl"

fixture_profile="$HOME/Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files/wxid_fixture_abcd"
fixture_db="$fixture_profile/db_storage/message/message_0.db"
fixture_media="$fixture_profile/msg/file/2026-06/sample.txt"
mkdir -p "$(dirname "$fixture_db")" "$(dirname "$fixture_media")"
printf 'sample media\n' > "$fixture_media"
python3 - "$fixture_db" <<'PY'
import pathlib
import sqlite3
import sys

db_path = pathlib.Path(sys.argv[1])
conn = sqlite3.connect(db_path)
conn.executescript("""
create table weicrawl_fixture_contacts (
  contact_id text primary key,
  alias text,
  display_name text,
  remark_name text,
  kind text,
  avatar_ref text,
  raw_json text
);
create table weicrawl_fixture_chats (
  chat_id text primary key,
  kind text,
  title text,
  last_message_at text,
  unread_count integer,
  muted integer,
  pinned integer,
  raw_json text
);
create table weicrawl_fixture_messages (
  message_id text primary key,
  chat_id text,
  sender_id text,
  direction text,
  message_type text,
  sent_at text,
  text text,
  normalized_text text,
  source_rowid text,
  raw_json text
);
insert into weicrawl_fixture_contacts values
  ('alice', 'alice', 'Alice', '', 'user', '', '{}');
insert into weicrawl_fixture_chats values
  ('chat-1', 'direct', 'Fixture Chat', '2026-06-13T01:00:00Z', 0, 0, 0, '{}');
insert into weicrawl_fixture_messages values
  ('msg-1', 'chat-1', 'alice', 'inbound', 'text', '2026-06-13T01:00:00Z', 'hello from e2e fixture', 'hello from e2e fixture', '1', '{}');
""")
conn.commit()
conn.close()
PY

go run ./cmd/weicrawl --json init > "$tmpdir/init.json"
go run ./cmd/weicrawl --json metadata > "$tmpdir/metadata.json"
go run ./cmd/weicrawl --json doctor > "$tmpdir/doctor.json"
env -u WEICRAWL_WECHAT_APP_ID -u WEICRAWL_WECHAT_APP_SECRET \
  go run ./cmd/weicrawl --json sync --source all --keep-source-snapshot > "$tmpdir/sync-all.json"
go run ./cmd/weicrawl --json status > "$tmpdir/status.json"
go run ./cmd/weicrawl --json unlock status > "$tmpdir/unlock-status.json"
go run ./cmd/weicrawl --json unlock scan-keys --allow-process-inspect > "$tmpdir/scan-plan.json"
go run ./cmd/weicrawl --json search "e2e fixture" > "$tmpdir/search.json"
go run ./cmd/weicrawl --json export --format jsonl --out "$tmpdir/archive.jsonl" > "$tmpdir/export-jsonl.json"
go run ./cmd/weicrawl --json export --format markdown --out "$tmpdir/markdown" > "$tmpdir/export-markdown.json"
go run ./cmd/weicrawl --json --db "$tmpdir/jsonl-import.db" init > "$tmpdir/jsonl-import-init.json"
go run ./cmd/weicrawl --json --db "$tmpdir/jsonl-import.db" import --format jsonl "$tmpdir/archive.jsonl" > "$tmpdir/import-jsonl.json"
go run ./cmd/weicrawl --json --db "$tmpdir/jsonl-import.db" search "e2e fixture" > "$tmpdir/search-jsonl-import.json"
go run ./cmd/weicrawl --json snapshot create --out "$tmpdir/snapshot" > "$tmpdir/snapshot-create.json"
go run ./cmd/weicrawl --json --db "$tmpdir/snapshot-import.db" init > "$tmpdir/snapshot-import-init.json"
go run ./cmd/weicrawl --json --db "$tmpdir/snapshot-import.db" import "$tmpdir/snapshot" > "$tmpdir/import-snapshot.json"
go run ./cmd/weicrawl --json --db "$tmpdir/snapshot-import.db" search "e2e fixture" > "$tmpdir/search-snapshot-import.json"
go run ./cmd/weicrawl --json tui > "$tmpdir/tui.json"

python3 - "$tmpdir" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
payloads = {path.stem: json.loads(path.read_text()) for path in root.glob("*.json")}

metadata = payloads["metadata"]
required_commands = {"init", "doctor", "metadata", "status", "sync", "unlock", "search", "export", "snapshot", "import", "tui"}
missing = sorted(required_commands - set(metadata.get("commands", {})))
if missing:
    raise SystemExit(f"metadata missing commands: {missing}")

sync = payloads["sync-all"]
if sync.get("source") != "all" or sync.get("status") not in {"success", "partial", "skipped"}:
    raise SystemExit(f"unexpected sync payload: {sync}")
if sync.get("errors"):
    raise SystemExit(f"sync reported errors: {sync['errors']}")

scan = payloads["scan-plan"]
notes = "\n".join(scan.get("plan", {}).get("notes", []))
if "per-database keys" not in notes:
    raise SystemExit(f"scan plan does not mention per-database keys: {scan}")
if not isinstance(scan.get("wechat_running"), bool):
    raise SystemExit(f"scan plan did not report wechat_running: {scan}")

status = payloads["status"]
if status.get("control", {}).get("state") != "ok":
    raise SystemExit(f"status not ok: {status}")

for name in ("search", "search-jsonl-import", "search-snapshot-import"):
    hits = payloads[name].get("hits", [])
    if not hits:
        raise SystemExit(f"{name} returned no hits: {payloads[name]}")

if payloads["export-jsonl"].get("rows", 0) <= 0:
    raise SystemExit(f"jsonl export wrote no rows: {payloads['export-jsonl']}")
if payloads["import-jsonl"].get("rows", 0) <= 0:
    raise SystemExit(f"jsonl import wrote no rows: {payloads['import-jsonl']}")
if not (root / "markdown" / "chat-1.md").exists():
    raise SystemExit("markdown export did not write chat-1.md")
if not (root / "snapshot" / "manifest.json").exists():
    raise SystemExit("snapshot export did not write manifest.json")

print(json.dumps({
    "sync": {
        "source": sync.get("source"),
        "status": sync.get("status"),
        "nested": [(item.get("source"), item.get("status")) for item in sync.get("results", [])],
    },
    "scan": {
        "available": scan.get("available"),
        "wechat_running": scan.get("wechat_running"),
    },
    "status": {
        "state": status.get("control", {}).get("state"),
        "warnings": len(status.get("warnings", [])),
    },
    "roundtrip": {
        "jsonl_rows": payloads["import-jsonl"].get("rows"),
        "search_hits": len(payloads["search"].get("hits", [])),
        "jsonl_import_hits": len(payloads["search-jsonl-import"].get("hits", [])),
        "snapshot_import_hits": len(payloads["search-snapshot-import"].get("hits", [])),
    },
}, indent=2))
PY

if [[ -n "${WEICRAWL_LIVE_KEYS:-}" || -n "${WEICRAWL_LIVE_SNAPSHOT:-}" ]]; then
  if [[ -z "${WEICRAWL_LIVE_KEYS:-}" || -z "${WEICRAWL_LIVE_SNAPSHOT:-}" ]]; then
    echo "WEICRAWL_LIVE_KEYS and WEICRAWL_LIVE_SNAPSHOT must be supplied together" >&2
    exit 2
  fi

  echo "== optional live copied-snapshot unlock probe =="
  go run ./cmd/weicrawl --json doctor \
    --probe-unlock \
    --probe-decrypt \
    --keys "$WEICRAWL_LIVE_KEYS" \
    --snapshot "$WEICRAWL_LIVE_SNAPSHOT" > "$tmpdir/live-doctor-unlock.json"
  go run ./cmd/weicrawl --json unlock desktop \
    --explain \
    --probe-decrypt \
    --keys "$WEICRAWL_LIVE_KEYS" \
    --snapshot "$WEICRAWL_LIVE_SNAPSHOT" > "$tmpdir/live-unlock-probe.json"

  if [[ "${WEICRAWL_LIVE_UNLOCK_SYNC:-0}" == "1" ]]; then
    go run ./cmd/weicrawl --json unlock desktop \
      --keys "$WEICRAWL_LIVE_KEYS" \
      --snapshot "$WEICRAWL_LIVE_SNAPSHOT" \
      --out "$tmpdir/decrypted" \
      --sync > "$tmpdir/live-unlock-sync.json"
    go run ./cmd/weicrawl --json status > "$tmpdir/live-status-after-unlock.json"
  fi
fi
