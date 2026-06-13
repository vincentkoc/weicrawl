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
  if [[ -n "${official_server_pid:-}" ]]; then
    kill "$official_server_pid" 2>/dev/null || true
    wait "$official_server_pid" 2>/dev/null || true
  fi
  rm -rf "$tmpdir"
}
trap cleanup EXIT

GOBIN="$tmpdir/bin" GOWORK=off go install ./cmd/weicrawl
weicrawl="$tmpdir/bin/weicrawl"

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

cat > "$tmpdir/official_api.py" <<'PY'
import http.server
import json
import pathlib
import socketserver
import sys
import urllib.parse

ready = pathlib.Path(sys.argv[1])

class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        return

    def do_GET(self):
        parsed = urllib.parse.urlparse(self.path)
        query = urllib.parse.parse_qs(parsed.query)
        if parsed.path != "/cgi-bin/token":
            self.send_error(404)
            return
        if query.get("appid") != ["official-app"] or query.get("secret") != ["official-secret"]:
            self.send_error(403)
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({"access_token": "official-token", "expires_in": 7200}).encode())

    def do_POST(self):
        parsed = urllib.parse.urlparse(self.path)
        query = urllib.parse.parse_qs(parsed.query)
        if parsed.path != "/cgi-bin/material/batchget_material":
            self.send_error(404)
            return
        if query.get("access_token") != ["official-token"]:
            self.send_error(403)
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({
            "total_count": 1,
            "item_count": 1,
            "item": [{
                "media_id": "official-media-1",
                "update_time": 1781323200,
                "content": {"news_item": [{
                    "title": "Official account e2e",
                    "digest": "Synthetic official account post",
                    "url": "https://example.invalid/official-e2e"
                }]}
            }]
        }).encode())

with socketserver.TCPServer(("127.0.0.1", 0), Handler) as httpd:
    ready.write_text(f"http://127.0.0.1:{httpd.server_address[1]}\n")
    httpd.serve_forever()
PY
python3 "$tmpdir/official_api.py" "$tmpdir/official-api-url" &
official_server_pid=$!
for _ in $(seq 1 50); do
  [[ -s "$tmpdir/official-api-url" ]] && break
  sleep 0.1
done
if [[ ! -s "$tmpdir/official-api-url" ]]; then
  echo "official-account fake API did not start" >&2
  exit 1
fi
official_base_url="$(cat "$tmpdir/official-api-url")"

"$weicrawl" --json init > "$tmpdir/init.json"
"$weicrawl" --json version > "$tmpdir/version.json"
"$weicrawl" --json metadata > "$tmpdir/metadata.json"
"$weicrawl" --json doctor > "$tmpdir/doctor.json"
env -u WEICRAWL_WECHAT_APP_ID -u WEICRAWL_WECHAT_APP_SECRET \
  "$weicrawl" --json sync --source all --keep-source-snapshot > "$tmpdir/sync-all.json"
snapshot_path="$(python3 - "$tmpdir/sync-all.json" <<'PY'
import json
import sys
payload = json.load(open(sys.argv[1]))
for item in payload.get("results", []):
    if item.get("source") == "desktop-macos" and item.get("snapshot_path"):
        print(item["snapshot_path"])
        break
PY
)"
if [[ -z "$snapshot_path" ]]; then
  echo "desktop sync did not produce a retained snapshot" >&2
  exit 1
fi
"$weicrawl" --json status > "$tmpdir/status.json"
"$weicrawl" --json unlock status > "$tmpdir/unlock-status.json"
"$weicrawl" --json unlock scan-keys --allow-process-inspect > "$tmpdir/scan-plan.json"
"$weicrawl" --json unlock template --snapshot "$snapshot_path" --out "$tmpdir/wechat_keys.template.json" > "$tmpdir/unlock-template.json"
env WEICRAWL_WECHAT_APP_ID=official-app \
  WEICRAWL_WECHAT_APP_SECRET=official-secret \
  WEICRAWL_WECHAT_API_BASE_URL="$official_base_url" \
  "$weicrawl" --json sync --source official-account-api > "$tmpdir/official-sync.json"
"$weicrawl" --json search "e2e fixture" > "$tmpdir/search.json"
"$weicrawl" --json search "Official account e2e" > "$tmpdir/search-official.json"
"$weicrawl" --json export --format jsonl --out "$tmpdir/archive.jsonl" > "$tmpdir/export-jsonl.json"
"$weicrawl" --json export --format markdown --out "$tmpdir/markdown" > "$tmpdir/export-markdown.json"
"$weicrawl" --json --db "$tmpdir/jsonl-import.db" init > "$tmpdir/jsonl-import-init.json"
"$weicrawl" --json --db "$tmpdir/jsonl-import.db" import --format jsonl "$tmpdir/archive.jsonl" > "$tmpdir/import-jsonl.json"
"$weicrawl" --json --db "$tmpdir/jsonl-import.db" search "e2e fixture" > "$tmpdir/search-jsonl-import.json"
"$weicrawl" --json snapshot create --out "$tmpdir/snapshot" > "$tmpdir/snapshot-create.json"
"$weicrawl" --json --db "$tmpdir/snapshot-import.db" init > "$tmpdir/snapshot-import-init.json"
"$weicrawl" --json --db "$tmpdir/snapshot-import.db" import "$tmpdir/snapshot" > "$tmpdir/import-snapshot.json"
"$weicrawl" --json --db "$tmpdir/snapshot-import.db" search "e2e fixture" > "$tmpdir/search-snapshot-import.json"
"$weicrawl" --json tui > "$tmpdir/tui.json"

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
required_capabilities = {"official-rate-limit-posture", "official-token-metadata-cache", "jsonl-export", "snapshot-export", "unlock-sync"}
missing_capabilities = sorted(required_capabilities - set(metadata.get("capabilities", [])))
if missing_capabilities:
    raise SystemExit(f"metadata missing capabilities: {missing_capabilities}")

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

template = payloads["unlock-template"]
if template.get("method") != "key-manifest-template" or template.get("db_count", 0) <= 0:
    raise SystemExit(f"unlock template did not enumerate copied DBs: {template}")
template_path = root / "wechat_keys.template.json"
if not template_path.exists():
    raise SystemExit("unlock template file was not written")
template_text = template_path.read_text()
if "REPLACE_WITH_64_HEX_SQLCIPHER_KEY" not in template_text:
    raise SystemExit("unlock template missing placeholder")

status = payloads["status"]
if status.get("control", {}).get("state") != "ok":
    raise SystemExit(f"status not ok: {status}")

official = payloads["official-sync"]
if official.get("status") != "success" or official.get("articles") != 1 or not official.get("token_cache_safe"):
    raise SystemExit(f"official-account sync failed contract: {official}")
if official.get("rate_limited") or official.get("raw_token_persisted"):
    raise SystemExit(f"official-account sync unsafe posture: {official}")

for name in ("search", "search-official", "search-jsonl-import", "search-snapshot-import"):
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
        "official_search_hits": len(payloads["search-official"].get("hits", [])),
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
  "$weicrawl" --json doctor \
    --probe-unlock \
    --probe-decrypt \
    --keys "$WEICRAWL_LIVE_KEYS" \
    --snapshot "$WEICRAWL_LIVE_SNAPSHOT" > "$tmpdir/live-doctor-unlock.json"
  "$weicrawl" --json unlock desktop \
    --explain \
    --probe-decrypt \
    --keys "$WEICRAWL_LIVE_KEYS" \
    --snapshot "$WEICRAWL_LIVE_SNAPSHOT" > "$tmpdir/live-unlock-probe.json"
  python3 - "$tmpdir/live-doctor-unlock.json" "$tmpdir/live-unlock-probe.json" <<'PY'
import json
import sys

doctor = json.load(open(sys.argv[1]))
probe = json.load(open(sys.argv[2]))

readiness = None
for check in doctor.get("checks", []):
    if check.get("id") == "unlock_readiness":
        readiness = check
        break
if not readiness or not readiness.get("ok"):
    raise SystemExit(f"doctor unlock readiness failed: {readiness}")

check = probe.get("check", {})
if not probe.get("available") or not check.get("ready") or not check.get("probe_ready"):
    raise SystemExit(f"live unlock probe did not prove keys: {probe}")
PY

  if [[ "${WEICRAWL_LIVE_UNLOCK_SYNC:-0}" == "1" ]]; then
    "$weicrawl" --json unlock desktop \
      --keys "$WEICRAWL_LIVE_KEYS" \
      --snapshot "$WEICRAWL_LIVE_SNAPSHOT" \
      --out "$tmpdir/decrypted" \
      --sync > "$tmpdir/live-unlock-sync.json"
    "$weicrawl" --json status > "$tmpdir/live-status-after-unlock.json"
    python3 - "$tmpdir/live-unlock-sync.json" "$tmpdir/live-status-after-unlock.json" <<'PY'
import json
import sys

unlock = json.load(open(sys.argv[1]))
status = json.load(open(sys.argv[2]))

sync = unlock.get("sync") or {}
if sync.get("status") != "success":
    raise SystemExit(f"live unlock sync did not succeed: {unlock}")
if sync.get("imported_messages", 0) <= 0:
    raise SystemExit(f"live unlock sync imported no messages: {unlock}")
if not unlock.get("decrypted_removed"):
    raise SystemExit(f"live unlock sync did not remove decrypted output by default: {unlock}")
archive = status.get("archive") or {}
if archive.get("message_count", 0) <= 0:
    raise SystemExit(f"post-unlock archive has no messages: {status}")
PY
  fi
fi
