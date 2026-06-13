---
name: weicrawl
description: Operate and validate the weicrawl Weixin/WeChat archive CLI, including pull/build/init, copied-snapshot key setup, unlock proof, and release readiness.
license: MIT
metadata:
  app: weicrawl
---

# Weicrawl

## Purpose

Use this skill when operating `weicrawl`, the local-first Weixin/WeChat archive
CLI. It covers install/pull/init, safe copied-snapshot key setup, decrypt/import
proof, and release readiness.

## Pull and Build

```bash
git clone https://github.com/vincentkoc/weicrawl
cd weicrawl
git pull --ff-only
GOWORK=off go test -count=1 ./...
go install ./cmd/weicrawl
```

For a local checkout that already exists:

```bash
cd /Users/vincentkoc/GIT/_Perso/weicrawl
git pull --ff-only
GOWORK=off go test -count=1 ./...
go install ./cmd/weicrawl
```

## Init

Start with JSON init. It creates config and the archive DB, then returns the key
setup guide:

```bash
weicrawl --json init
weicrawl --json doctor
```

Use temp homes for agent tests. Do not mutate live WeChat data.

## Key Setup

`weicrawl` does not ship a WeChat memory scanner and does not attach to WeChat
by default. The key flow is:

```bash
weicrawl --json sync --source desktop-macos --keep-source-snapshot
weicrawl --json init \
  --snapshot ~/.cache/weicrawl/snapshots/<run-id>/<profile> \
  --key-template-out ./wechat_keys.template.json
weicrawl --json unlock scan-keys \
  --allow-process-inspect \
  --execute \
  --script ./scripts/wechat-key-scan-nosip.sh \
  --scan-out ./wechat_keys.json
weicrawl --json init \
  --snapshot ~/.cache/weicrawl/snapshots/<run-id>/<profile> \
  --keys ./wechat_keys.json \
  --probe-decrypt
```

Before using the no-SIP wrapper, run:

```bash
sudo DevToolsSecurity -enable
export WEICRAWL_WECHAT_KEY_HELPER_ROOT=/path/to/wechat-db-decrypt-macos
```

Only run the scanner command when the operator explicitly permits process
inspection. Never commit `wechat_keys.json`, decrypted DBs, real snapshots, or
private logs. Do not disable SIP for the normal `weicrawl` flow; if Developer
Tools authorization still denies attach, stop and report that the selected
extractor cannot run safely on the current machine.

## Import

After keys validate and probe successfully:

```bash
weicrawl --json unlock desktop \
  --keys ./wechat_keys.json \
  --snapshot ~/.cache/weicrawl/snapshots/<run-id>/<profile> \
  --out ./decrypted \
  --sync
weicrawl --json status
weicrawl --json search "<query>"
```

By default, `--sync` removes decrypted output after import.

## Release Gate

Dry-run packaging without live key proof:

```bash
WEICRAWL_RELEASE_TAG=v0.1.0 ./scripts/release-check.sh --allow-missing-live
```

Real release readiness requires a real key manifest and copied-snapshot
decrypt/import proof:

```bash
WEICRAWL_RELEASE_TAG=v0.1.0 \
WEICRAWL_LIVE_KEYS=./wechat_keys.json \
./scripts/release-check.sh
```

Do not tag or publish until the live proof imports real messages from a copied
snapshot.
