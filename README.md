# weicrawl

`weicrawl` is a local-first Weixin/WeChat archive CLI.

It keeps the product boundary sharp:

- read-only against WeChat app data
- SQLite archive under `~/.config/weicrawl`
- copied snapshots before parsing
- explicit unlock/decrypt flow for encrypted desktop databases
- no hidden process inspection
- no raw keys, access tokens, or real chat fixtures in git

## status

Implemented:

- config + SQLite archive schema
- `init`, `doctor`, `metadata`, `status`, `sync`, `unlock`, `search`, `sql`,
  `export`, `snapshot`, `import`, `tui`, and list commands
- macOS WeChat app/container/profile/database discovery
- read-only DB/WAL/SHM snapshot copy
- user-selected `desktop-backup` root import
- metadata-only or copied media indexing
- decrypted/native WeChat table importer for:
  - `contact`
  - `stranger`
  - `SessionTable`
  - `Name2Id`
  - `Msg_<md5(username)>`
  - native `gh_` public-account chats/accounts
  - rich link/media payloads inside native messages
  - conservative favorite/moment table shapes when recognizable
- `wechat_keys.json` manifest decrypt workflow using `sqlcipher`
- explicit `unlock scan-keys` planner/wrapper for reviewed external extractors
- official-account token and news-material ingestion path
- synthetic e2e coverage

Not done until proven live:

- extracting WeChat 4.1.x SQLCipher keys
- live decrypt/import against this machine's real copied WeChat snapshot
- live proof for native favorites, biz messages, moments, and rich media records
- broader native schema coverage for version-specific table variants

## quick start

```bash
go run ./cmd/weicrawl --json init
go run ./cmd/weicrawl --json doctor
go run ./cmd/weicrawl --json sync --source desktop-macos --keep-source-snapshot
go run ./cmd/weicrawl --json status
go run ./cmd/weicrawl --json search --since 30d "invoice"
go run ./cmd/weicrawl --json tui --scope all
```

The default desktop sync copies local DBs first. If they are encrypted, the sync
records the profile and snapshot provenance, then warns that no readable tables
were imported.

To run every configured local source plus any explicitly selected artifact
sources:

```bash
go run ./cmd/weicrawl --json sync --source all --keep-source-snapshot
```

`--source all` includes `desktop-macos` when enabled in config. It includes
`desktop-backup` only when `--backup-root` is supplied, import artifacts only
when `--import-path` is supplied, and `official-account-api` only when that
source is enabled in config, so desktop-local syncs do not make surprise network
calls just because credentials exist in the environment.

Backup or migration directories are only read when selected explicitly:

```bash
go run ./cmd/weicrawl --json sync \
  --source desktop-backup \
  --backup-root /path/to/copied/backup
```

## decrypt/import flow

`weicrawl` does not attach to WeChat or scan process memory by default. Use a
reviewed external key-extraction path to produce `wechat_keys.json`, then
decrypt the copied snapshot:

```bash
brew install sqlcipher

go run ./cmd/weicrawl --json unlock scan-keys \
  --allow-process-inspect \
  --execute \
  --script /path/to/find_key_memscan.py \
  --scan-out ./wechat_keys.json

go run ./cmd/weicrawl --json sync \
  --source desktop-macos \
  --keep-source-snapshot

go run ./cmd/weicrawl --json unlock desktop \
  --explain \
  --keys ./wechat_keys.json \
  --snapshot ~/.cache/weicrawl/snapshots/<run-id>/<profile>

go run ./cmd/weicrawl --json unlock desktop \
  --keys ./wechat_keys.json \
  --snapshot ~/.cache/weicrawl/snapshots/<run-id>/<profile> \
  --out ./decrypted

go run ./cmd/weicrawl --json sync \
  --source desktop-macos \
  --profile <profile> \
  --decrypted-dir ./decrypted
```

`wechat_keys.json` may either map individual copied database paths to keys or
provide one profile key:

```json
{
  "__default_key": "<64-hex-sqlcipher-key>"
}
```

The default key is applied to every `.db` file under the copied snapshot's
`db_storage` tree. Per-database entries override it when needed.

`wechat_keys.json` and decrypted DBs are ignored by git.

## exports

JSONL:

```bash
go run ./cmd/weicrawl --json export --format jsonl --out exports/archive.jsonl
go run ./cmd/weicrawl --json export --format jsonl --scope messages --out exports/messages.jsonl
go run ./cmd/weicrawl --json import --format jsonl exports/archive.jsonl
go run ./cmd/weicrawl --json sync --source import --import-path exports/archive.jsonl
```

Markdown:

```bash
go run ./cmd/weicrawl --json export --format markdown --out exports/markdown
```

## validation

```bash
GOWORK=off go mod tidy
git diff --exit-code -- go.mod go.sum
GOWORK=off go vet ./...
GOWORK=off go test -count=1 ./...
```

Tests use temp homes and synthetic SQLite fixtures only. They must not read or
mutate the operator's live WeChat container.
