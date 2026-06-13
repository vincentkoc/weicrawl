# weicrawl spec

## intent

Build `weicrawl` as the local-first archive tool for Weixin/WeChat messages,
contacts, public-account articles, favorites, media metadata, and selected
timeline/business records.

The product should feel like the other crawl apps:

- local SQLite archive
- deterministic read-only sync runs
- stable JSON command surfaces for agents and scripts
- optional terminal browser through `crawlkit/tui`
- portable private snapshots through `crawlkit/snapshot`
- strict handling of live app stores, decrypted data, and secrets

The hard part is not the CLI. The hard part is the source boundary. WeChat
desktop data is local and useful, but encrypted, app-version-sensitive, and
personal. `weicrawl` should make access explicit, inspectable, and reversible.
No hidden background extractor. No mutation of WeChat files. No surprise
Keychain, debugger, or process-memory access.

## repository identity

Initial local identity:

- repo: `github.com/vincentkoc/weicrawl`
- module: `github.com/vincentkoc/weicrawl`
- binary: `weicrawl`
- language: Go for the CLI/archive
- helper language: small macOS helper only when encrypted desktop unlock needs
  platform APIs or debugger/process inspection
- shared library: `github.com/openclaw/crawlkit`
- default app dir: `~/.config/weicrawl`
- default database: `~/.config/weicrawl/weicrawl.db`
- default cache dir: `~/.cache/weicrawl`
- default logs dir: `~/.local/state/weicrawl/logs`

Future OpenClaw move:

- before the first public OpenClaw release, rewrite module/install docs to
  `github.com/openclaw/weicrawl`
- do not publish stable tags under `github.com/vincentkoc/weicrawl` unless the
  temporary namespace is intentionally supported
- keep package imports internal-heavy so the repo transfer is mostly module and
  release metadata churn

## local findings

Verified on this machine without reading message contents:

- macOS app: `/Applications/WeChat.app`
- bundle id: `com.tencent.xinWeChat`
- version: `4.1.10`
- sandbox container:
  `~/Library/Containers/com.tencent.xinWeChat`
- profile root pattern:
  `~/Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files/<profile>`
- observed archive areas:
  - `db_storage/message`
  - `db_storage/contact`
  - `db_storage/session`
  - `db_storage/favorite`
  - `db_storage/sns`
  - `db_storage/bizchat`
  - `db_storage/message_resource`
  - `msg/file`
  - `msg/video`
  - `msg/attach`
  - `cache/<yyyy-mm>/Message`
  - `cache/<yyyy-mm>/HttpResource`
  - `all_users/login/<wxid>/key_info.db`

Treat these as discovery hints, not a stable contract. WeChat version changes
can move tables, split shards, change encryption, or rename storage roots.

## product stance

Default to local read-only desktop archival.

Support official account APIs only as a separate adapter for accounts the
operator controls. Do not mix official-account assets with personal chats unless
the user explicitly asks for a joined archive.

Use a two-stage desktop pipeline:

1. snapshot live WeChat files into `~/.cache/weicrawl/snapshots/<run-id>`
2. parse only the snapshot into `weicrawl.db`

Unlocking encrypted desktop data is explicit. A normal `status`, `search`,
`tui`, or `export` command must never attach to WeChat, prompt Keychain, inspect
process memory, or mutate any Tencent-owned file.

## non-goals

- no write-back to WeChat
- no sending messages
- no moderation, automation, bot, or accessibility-control write path in v1
- no hosted service in v1
- no browser UI in v1
- no mutation of WeChat app databases, WAL files, caches, preferences, backups,
  or key stores
- no raw key, token, cookie, or decrypted source database persistence outside
  the operator-approved cache
- no bypassing another user's account, device, workspace, phone, or enterprise
  policy
- no crawlkit provider-specific code
- no public release until the legal/privacy copy and unlock warnings are good

## source adapters

### `desktop-macos`

Primary v1 source.

Reads WeChat for macOS local files through a copied snapshot. The source
adapter owns all WeChat-specific discovery, schema mapping, decryption handling,
message normalization, and media lookup.

Discovery should find:

- installed app path and version
- sandbox container path
- profile roots under `xwechat_files`
- available database shards
- related media/cache directories
- backup directories
- whether WeChat is currently running

Default behavior:

- copy candidate DB files plus WAL/SHM sidecars into a temp snapshot
- copy only needed metadata/media sidecars unless `--include-media` is set
- open copied DBs read-only
- fail clearly when a DB is encrypted and no unlock material is available
- record source file fingerprints and app version in `sync_runs`

Unlock behavior:

- `weicrawl unlock desktop` is the only command allowed to cross the encrypted
  local-data boundary
- every unlock method must be named in output before it runs
- any debugger/process-memory/keychain path requires an explicit flag such as
  `--allow-process-inspect`
- unlocked keys live in memory by default
- optional key persistence must use the OS credential store and be opt-in
- decrypted source DB copies are temporary by default and deleted after ingest
- `--keep-decrypted-snapshot` is debug-only and must print the output path

Candidate entities:

- accounts/profiles
- contacts
- chat sessions
- one-to-one messages
- group messages
- public-account/business messages
- favorites
- media metadata
- file/video/attachment references
- message resources/cards
- moments/timeline records when locally available and understandable
- raw source records for unsupported or version-specific rows

### `desktop-backup`

Optional source for WeChat backup/migration artifacts under copied local backup
directories.

This adapter should be conservative:

- never restore into WeChat
- never modify backup files
- import only from user-selected backup roots
- store backup provenance separately from live desktop snapshots

### `official-account-api`

Optional source for public accounts the operator owns or administers.

This is not a personal chat crawler. It should live behind its own config block
and table namespace.

Candidate data:

- account profile metadata
- permanent media/material list and selected material payloads
- drafts or published article metadata where the account has access
- user/comment/statistics surfaces only when credentials and platform rules
  allow it

Auth:

- `WEICRAWL_WECHAT_APP_ID`
- `WEICRAWL_WECHAT_APP_SECRET`
- optional configured credential names
- no secrets in repo fixtures, snapshots, logs, or JSON output

Rules:

- centralize access-token fetch/cache/refresh
- obey platform rate limits and error codes
- do not require this adapter for desktop-local archive use
- keep official-account records distinct from private personal-chat rows

### `import`

Reads existing exported artifacts generated by `weicrawl` or manually supplied
JSONL/SQLite fixtures.

This is the compatibility valve for early reverse-engineering churn. If a
desktop parser changes, users should still be able to import a prior normalized
snapshot without needing old WeChat binaries.

## SQLite archive

SQLite is canonical. Source DBs and media files are inputs, not the archive.

Store startup must use the same hygiene as the other crawl apps:

- WAL mode
- foreign keys on
- busy timeout
- normal synchronous writes
- temp-store tuning where safe
- schema version table
- deterministic migration order
- temp directories for tests

Core tables:

- `profiles`
- `contacts`
- `chats`
- `chat_members`
- `messages`
- `message_parts`
- `message_events`
- `media_items`
- `favorites`
- `biz_accounts`
- `biz_articles`
- `moments`
- `raw_records`
- `sync_runs`
- `sync_state`
- `message_fts`
- `article_fts`

Optional later:

- `message_embeddings`
- `article_embeddings`
- `message_entities`
- `media_blobs`

### `profiles`

```sql
create table profiles (
  profile_id text primary key,
  wxid text,
  display_name text,
  source_root text not null,
  app_version text,
  raw_json text not null,
  updated_at text not null
);
```

### `contacts`

```sql
create table contacts (
  profile_id text not null,
  contact_id text not null,
  alias text,
  display_name text,
  remark_name text,
  kind text not null,
  avatar_ref text,
  raw_json text not null,
  updated_at text not null,
  primary key (profile_id, contact_id)
);
```

`kind` values should start with:

- `user`
- `group`
- `public_account`
- `service_account`
- `unknown`

### `chats`

```sql
create table chats (
  profile_id text not null,
  chat_id text not null,
  kind text not null,
  title text,
  last_message_at text,
  unread_count integer not null default 0,
  muted integer not null default 0,
  pinned integer not null default 0,
  raw_json text not null,
  updated_at text not null,
  primary key (profile_id, chat_id)
);
```

`kind` values should start with:

- `direct`
- `group`
- `public_account`
- `bizchat`
- `system`
- `unknown`

### `messages`

```sql
create table messages (
  profile_id text not null,
  message_id text not null,
  chat_id text not null,
  sender_id text,
  direction text not null,
  message_type text not null,
  sent_at text,
  edited_at text,
  deleted_at text,
  text text not null default '',
  normalized_text text not null default '',
  source_db text not null,
  source_rowid text not null,
  raw_json text not null,
  updated_at text not null,
  primary key (profile_id, message_id)
);
```

`direction` values:

- `inbound`
- `outbound`
- `system`
- `unknown`

`message_type` should preserve source specificity but normalize common cases:

- `text`
- `image`
- `voice`
- `video`
- `file`
- `link`
- `mini_program`
- `sticker`
- `location`
- `red_packet`
- `transfer`
- `quoted`
- `system`
- `unsupported`

### `message_parts`

Use this for mixed cards, attachments, quoted messages, translated text, and
future source-specific expansions without bloating `messages`.

```sql
create table message_parts (
  profile_id text not null,
  message_id text not null,
  part_index integer not null,
  kind text not null,
  text text not null default '',
  media_id text,
  url text,
  raw_json text not null,
  primary key (profile_id, message_id, part_index)
);
```

### `media_items`

```sql
create table media_items (
  profile_id text not null,
  media_id text not null,
  kind text not null,
  source_path text,
  archive_path text,
  mime_type text,
  byte_size integer,
  sha256 text,
  width integer,
  height integer,
  duration_ms integer,
  raw_json text not null,
  updated_at text not null,
  primary key (profile_id, media_id)
);
```

Default media behavior is metadata-only. Blob copying must be opt-in.

### `raw_records`

Unsupported source rows should not be silently lost.

```sql
create table raw_records (
  id integer primary key autoincrement,
  profile_id text not null,
  source_name text not null,
  source_table text not null,
  source_key text not null,
  record_kind text not null,
  payload_json text not null,
  observed_at text not null
);
```

## search design

V1 search mode is FTS5 over normalized text.

Index:

- message body
- sender/contact display names
- chat titles
- public-account article titles and snippets
- favorites text
- supported card/link titles
- filename and media metadata text

Normalize:

- CJK text without lossy transliteration
- emoji and stickers as readable placeholders
- mentions and group sender names
- URLs and link cards
- mini-program cards
- quoted/replied message context
- deleted/edited/system markers

Embeddings are later. When added, use `crawlkit/embed` and `crawlkit/vector`
instead of app-local provider clients.

## command surface

Usage:

```text
weicrawl [global flags] <command> [args]
```

Global flags:

- `--config <path>`
- `--db <path>`
- `--profile <id-or-wxid>`
- `--json`
- `--quiet`
- `--verbose`

Public commands:

- `version`
- `init`
- `doctor`
- `metadata`
- `status`
- `sync`
- `unlock`
- `profiles`
- `contacts`
- `chats`
- `messages`
- `search`
- `favorites`
- `articles`
- `media`
- `runs`
- `sql`
- `export`
- `snapshot`
- `import`
- `tui`
- `completion`

### `doctor`

Must check:

- config readability
- archive DB openability
- migration state
- WeChat app presence and version
- sandbox container presence
- profile discovery
- database shard discovery
- whether source DBs appear encrypted
- whether an unlock method is configured
- whether any decrypted snapshot retention is enabled
- FTS health
- crawlkit metadata validity

`doctor` must not unlock encrypted data unless explicitly given
`--probe-unlock`. When `--probe-unlock --keys <manifest> --snapshot
<copied-profile-root>` is supplied, it should run the same dry-run readiness
check as `unlock desktop --explain` and still avoid writing decrypted files.

### `sync`

Purpose:

- copy a read-only snapshot and ingest normalized records

Expected flags:

- `--source desktop-macos|desktop-backup|official-account-api|import|all`
- `--profile <id-or-wxid>`
- `--since <timestamp>`
- `--full`
- `--include-media`
- `--media-mode metadata|copy`
- `--keep-source-snapshot`
- `--keep-decrypted-snapshot`
- `--concurrency <n>`

Rules:

- source snapshot first, ingest second
- never open live DBs for long-running queries
- never write into live WeChat roots
- store run provenance
- skip unsupported rows into `raw_records`
- produce stable JSON summaries
- `--source all` runs enabled configured sources plus explicit artifact sources;
  it must not call the official-account network API unless that source is
  enabled in config

### `unlock`

Purpose:

- explicitly cross the encrypted desktop-data boundary

Subcommands:

- `unlock desktop --profile <id-or-wxid>`
- `unlock forget --profile <id-or-wxid>`
- `unlock status`

Flags:

- `--keys <wechat_keys.json>`
- `--snapshot <copied-profile-root>`
- `--out <decrypted-dir>`
- `--sqlcipher <path>`
- `--allow-process-inspect`
- `--allow-keychain`
- `--store-keychain`
- `--once`
- `--explain`
- `--sync`
- `--keep-decrypted-snapshot`

`unlock desktop --explain --keys <manifest> --snapshot <copied-profile-root>`
is a dry-run readiness check. It verifies the manifest shape, matching snapshot
DB paths, and `sqlcipher` availability without writing decrypted files. Running
without `--explain` requires `--out` and performs the decrypt.

`unlock desktop --sync` should ingest the decrypted output into the configured
archive immediately after decrypting the copied snapshot. It must still require
explicit key material and a copied snapshot path; it must not read or mutate
live WeChat databases. Decrypted output should be removed after successful
`--sync` import unless `--keep-decrypted-snapshot` is supplied.

Key manifests must support both explicit per-database keys and one profile
default key:

```json
{
  "__default_key": "<64-hex-sqlcipher-key>",
  "keys": {
    "message/message_0.db": "<optional-override-key>"
  }
}
```

Default-key expansion is limited to `.db` files under the copied snapshot's
`db_storage` tree. Manifest database paths must be relative and must not escape
the decrypted output root after resolution. Per-database keys may be emitted as
snapshot-relative paths, `db_storage/...` paths, or absolute scanner paths that
contain a `db_storage` segment; decrypt/import must resolve them to copied
snapshot-relative paths before reading or writing files.

`unlock scan-keys --execute --scan-out <path>` should convert a 64-hex key from
the reviewed extractor's output into a `__default_key` manifest at `<path>`,
write it with private file permissions, and return only redacted scanner output.
If the extractor writes a valid manifest itself, the command may reuse it.
If the extractor prints a valid manifest JSON object to stdout, including stdout
with surrounding logs, the command should write that manifest to `<path>` instead
of collapsing it into a default key.
`--script` may point to a Python script or an executable helper; Python scripts
are run through `python3`, while executable helpers are run directly. Scanner
helpers should receive `WEICRAWL_SCAN_OUT` and `WEICRAWL_KEY_MANIFEST`
environment variables pointing at the requested manifest path.

Output must include:

- unlock method selected
- app version targeted
- profile targeted
- whether anything was persisted
- next command to run

Output must never include raw keys.

### `status`

Must include:

- profile count
- contact count
- chat count by kind
- message count by kind
- first and last message timestamp
- media metadata count
- favorites count
- public-account article count
- last sync run
- source app version
- snapshot/decryption retention status

### `search`

Examples:

```text
weicrawl search "invoice" --chat "OpenClaw" --since 30d --json
weicrawl search "航班" --from "Alice" --limit 20
weicrawl search --kind file "contract" --json
```

Search defaults to local SQLite only. It must not trigger unlock or sync unless
the user passes a future `--sync-if-stale` style flag.

### `tui`

Use `crawlkit/tui` over normalized rows.

Default layout:

- left pane: chats or public accounts
- middle pane: messages/articles
- right pane: detail

Required behavior:

- keyboard-first navigation
- mouse support when terminal allows it
- right-click action menus as polish, not required flow
- copy/open actions for URLs and source media paths
- compact rendering for dense group chats

## config spec

Format: TOML.

Default path:

```text
~/.config/weicrawl/config.toml
```

Example:

```toml
[archive]
db_path = "~/.config/weicrawl/weicrawl.db"
cache_dir = "~/.cache/weicrawl"
log_dir = "~/.local/state/weicrawl/logs"

[desktop_macos]
enabled = true
container_path = "~/Library/Containers/com.tencent.xinWeChat"
snapshot_mode = "copy"
media_mode = "metadata"
keep_source_snapshots = false
keep_decrypted_snapshots = false

[unlock]
allow_process_inspect = false
allow_keychain = false
store_keychain = false

[official_account]
enabled = false
app_id_env = "WEICRAWL_WECHAT_APP_ID"
app_secret_env = "WEICRAWL_WECHAT_APP_SECRET"
```

Primary environment variables:

- `WEICRAWL_CONFIG`
- `WEICRAWL_DB_PATH`
- `WEICRAWL_CACHE_DIR`
- `WEICRAWL_WECHAT_APP_ID`
- `WEICRAWL_WECHAT_APP_SECRET`
- `WEICRAWL_NO_UPDATE_CHECK`
- `CRAWLKIT_NO_UPDATE_CHECK`

## package layout

```text
cmd/weicrawl
internal/cli
internal/config
internal/store
internal/source/desktopmac
internal/source/backup
internal/source/officialaccount
internal/source/importer
internal/unlock
internal/schema
internal/syncer
internal/search
internal/media
internal/export
internal/tui
internal/version
```

Use `crawlkit` for:

- config path helpers where they fit
- SQLite hygiene helpers
- output JSON/text helpers
- control metadata
- release checks
- snapshots
- optional git mirrors later
- TUI
- embeddings/vector search later

Keep app-owned:

- WeChat app discovery
- WeChat DB/key/decryption behavior
- WeChat schema parsers
- WeChat message normalization
- public-account API client
- privacy filters
- CLI command contract

## security and privacy model

Principles:

- local-first
- read-only against WeChat
- explicit unlock
- no raw secrets in output
- no personal identifiers in fixtures
- no real chat text in tests
- no decrypted source DB checked into git
- no surprise external network calls from desktop-local commands

Sensitive values:

- wxid/profile ids
- contact aliases
- group names
- message text
- file paths
- media filenames
- SQLCipher/decryption keys
- app secrets/access tokens

Fixture policy:

- synthetic fixtures only
- placeholders for wxids and chat ids
- generated media blobs only
- no screenshots from real chats
- no copied Tencent databases

Log policy:

- redact paths below `xwechat_files/<profile>`
- redact access tokens, app secrets, keys, cookies, and auth headers
- redact message text in debug logs unless `--log-content` is explicitly set

## distribution

Local/dev phase:

- install with `go install ./cmd/weicrawl`
- no public release requirement
- no Homebrew formula until OpenClaw namespace move

OpenClaw release phase:

- module path: `github.com/openclaw/weicrawl`
- GitHub releases through GoReleaser
- source-built Homebrew formula in `openclaw/tap`
- passive update checks through `crawlkit/releasecheck`
- release notes must call out desktop unlock risk and supported WeChat versions

## validation

Before handoff:

```bash
GOWORK=off go mod tidy
git diff --exit-code -- go.mod go.sum
GOWORK=off go vet ./...
GOWORK=off go test -count=1 ./...
```

Desktop-source tests must use temp dirs and copied synthetic fixtures. They must
never read or mutate the operator's live WeChat container.

Manual smoke with temp homes:

```bash
tmp="$(mktemp -d)"
HOME="$tmp/home" XDG_CONFIG_HOME="$tmp/config" XDG_CACHE_HOME="$tmp/cache" weicrawl init --json
HOME="$tmp/home" XDG_CONFIG_HOME="$tmp/config" XDG_CACHE_HOME="$tmp/cache" weicrawl doctor --json
HOME="$tmp/home" XDG_CONFIG_HOME="$tmp/config" XDG_CACHE_HOME="$tmp/cache" weicrawl status --json
HOME="$tmp/home" XDG_CONFIG_HOME="$tmp/config" XDG_CACHE_HOME="$tmp/cache" weicrawl tui --json
```

Real desktop smoke is opt-in only:

```bash
weicrawl doctor
weicrawl sync --source desktop-macos --profile <profile> --full --json
weicrawl status --json
weicrawl search "test" --json
```

## build phases

### phase 0: repo scaffold

- `go.mod` under `github.com/vincentkoc/weicrawl`
- `cmd/weicrawl`
- config loader
- SQLite open/migrate
- JSON output
- `version`, `init`, `doctor`, `metadata`, `status`
- synthetic fixture tests

### phase 1: desktop discovery and snapshot

- detect WeChat app/container/profile roots
- list source DB candidates and media dirs
- copy DB/WAL/SHM sidecars into temp snapshots
- record run/source fingerprints
- no decryption yet

### phase 2: parser skeleton

- parse any unencrypted or unlocked copied DBs
- normalize contacts, chats, messages, favorites, and raw records
- populate FTS
- add `search`, `contacts`, `chats`, `messages`, `favorites`

### phase 3: explicit unlock

- implement `unlock desktop`
- version-gated unlock methods
- no persisted raw keys by default
- ingest encrypted desktop snapshots after explicit approval

### phase 4: media and exports

- media metadata indexing
- opt-in media copying
- Markdown/JSONL export
- crawlkit snapshot create/import

### phase 5: TUI and polish

- `crawlkit/tui` chat browser
- right-click/open/copy actions
- compact group rendering
- doctor/report quality pass

### phase 6: optional official-account adapter

- app-id/app-secret config
- access token cache
- material/article metadata sync
- separate table namespace from personal chats

## open questions

- Which WeChat surfaces matter first: personal messages, group chats,
  public-account articles, favorites, moments, files, or all of them?
- Should v1 include media blob copying or stay metadata-only until search works?
- Is process inspection acceptable on your dev machine for explicit unlock, or
  should the first pass require externally supplied keys/decrypted fixture DBs?
- Should `weicrawl` support Windows early, or follow the crawl-app pattern and
  make macOS solid first?

## sharp recommendation

Build v1 in this order:

1. scaffold CLI/archive/status with synthetic fixtures
2. desktop discovery and snapshot copy
3. parser against copied unlocked/decrypted fixtures
4. search and TUI
5. explicit unlock

Do not start with key extraction. That path is brittle and seductive. The
archive contract should be right before the risky adapter gets clever.
