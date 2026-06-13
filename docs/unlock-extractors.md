# Unlock extractor contract

`weicrawl` does not ship a WeChat memory scanner. The app owns copied-snapshot
decryption, import, redaction, and release proof. Key extraction stays in a
reviewed external helper because current macOS WeChat 4.x extractors generally
attach to the running WeChat process or scan its memory.

The contract is intentionally small:

```bash
weicrawl --json unlock scan-keys \
  --allow-process-inspect \
  --execute \
  --script /path/to/reviewed-helper \
  --scan-out ./wechat_keys.json
```

`--allow-process-inspect` is required for every scanner run. Passing it only
allows `weicrawl` to execute the helper command; it does not make `weicrawl`
attach to WeChat itself.

## Helper input

Helpers receive these environment variables:

- `WEICRAWL_SCAN_OUT`: requested manifest path
- `WEICRAWL_KEY_MANIFEST`: same value, kept for helpers that use a manifest name

Python helper paths ending in `.py` are run with `python3`. Other helper paths
are executed directly.

## Helper output

Preferred output is a `wechat_keys.json` manifest written to
`$WEICRAWL_SCAN_OUT`:

```json
{
  "keys": {
    "message/message_0.db": "<64-hex-sqlcipher-key>"
  }
}
```

For single-key profiles, helpers may write:

```json
{
  "__default_key": "<64-hex-sqlcipher-key>"
}
```

Helpers may also print either a full manifest JSON object or a single 64-hex key
to stdout. `weicrawl` redacts stdout before returning JSON and writes the parsed
manifest to `--scan-out`. A helper-written manifest wins over stdout so
per-database keys are not collapsed into a default key.

Accepted per-database keys:

- snapshot-relative paths: `message/message_0.db`
- copied paths: `db_storage/message/message_0.db`
- absolute scanner paths containing a `db_storage` segment

Do not commit `wechat_keys.json`.

## Probe path

After a helper writes keys, prove them against a copied snapshot:

```bash
weicrawl --json sync --source desktop-macos --keep-source-snapshot
weicrawl --json unlock template \
  --snapshot ~/.cache/weicrawl/snapshots/<run-id>/<profile> \
  --out ./wechat_keys.template.json
weicrawl --json unlock validate \
  --keys ./wechat_keys.json \
  --snapshot ~/.cache/weicrawl/snapshots/<run-id>/<profile>
weicrawl --json unlock desktop \
  --explain \
  --probe-decrypt \
  --keys ./wechat_keys.json \
  --snapshot ~/.cache/weicrawl/snapshots/<run-id>/<profile>
```

Only after that should release readiness run the importing proof:

```bash
WEICRAWL_RELEASE_TAG=v0.1.0 \
WEICRAWL_LIVE_KEYS=./wechat_keys.json \
./scripts/release-check.sh
```

For adapter testing without touching WeChat, use
`scripts/wechat-key-scanner.example.py`. It writes a manifest from
`WEICRAWL_WECHAT_SQLCIPHER_KEY` and is not a real extractor.
