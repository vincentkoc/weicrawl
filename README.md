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
- metadata-only or copied media indexing
- decrypted/native WeChat table importer for:
  - `contact`
  - `stranger`
  - `SessionTable`
  - `Name2Id`
  - `Msg_<md5(username)>`
- `wechat_keys.json` manifest decrypt workflow using `sqlcipher`
- explicit `unlock scan-keys` planner/wrapper for reviewed external extractors
- official-account token and news-material ingestion path
- synthetic e2e coverage

Not done until proven live:

- extracting WeChat 4.1.x SQLCipher keys
- live decrypt/import against this machine's real copied WeChat snapshot
- broader native schema coverage for favorites, biz messages, moments, and rich
  media records

## quick start

```bash
go run ./cmd/weicrawl --json init
go run ./cmd/weicrawl --json doctor
go run ./cmd/weicrawl --json sync --source desktop-macos --keep-source-snapshot
go run ./cmd/weicrawl --json status
```

The default desktop sync copies local DBs first. If they are encrypted, the sync
records the profile and snapshot provenance, then warns that no readable tables
were imported.

## decrypt/import flow

`weicrawl` does not attach to WeChat or scan process memory by default. Use a
reviewed external key-extraction path to produce `wechat_keys.json`, then
decrypt the copied snapshot:

```bash
brew install sqlcipher

go run ./cmd/weicrawl --json unlock scan-keys \
  --allow-process-inspect \
  --script /path/to/find_key_memscan.py

go run ./cmd/weicrawl --json sync \
  --source desktop-macos \
  --keep-source-snapshot

go run ./cmd/weicrawl --json unlock desktop \
  --keys ./wechat_keys.json \
  --snapshot ~/.cache/weicrawl/snapshots/<run-id>/<profile> \
  --out ./decrypted

go run ./cmd/weicrawl --json sync \
  --source desktop-macos \
  --profile <profile> \
  --decrypted-dir ./decrypted
```

`wechat_keys.json` and decrypted DBs are ignored by git.

## exports

JSONL:

```bash
go run ./cmd/weicrawl --json export --format jsonl --out exports/messages.jsonl
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
