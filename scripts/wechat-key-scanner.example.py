#!/usr/bin/env python3
"""Non-invasive scanner adapter fixture for `weicrawl unlock scan-keys`.

This is not a WeChat key extractor. It only proves the helper contract by
writing a manifest from WEICRAWL_WECHAT_SQLCIPHER_KEY.
"""

import json
import os
import pathlib
import re
import sys


def main() -> int:
    out_path = os.environ.get("WEICRAWL_SCAN_OUT") or os.environ.get("WEICRAWL_KEY_MANIFEST")
    key = os.environ.get("WEICRAWL_WECHAT_SQLCIPHER_KEY", "").strip()
    rel = os.environ.get("WEICRAWL_WECHAT_DB_REL", "").strip()

    if not out_path:
        print("WEICRAWL_SCAN_OUT is required", file=sys.stderr)
        return 2
    if not re.fullmatch(r"[0-9a-fA-F]{64}", key):
        print("WEICRAWL_WECHAT_SQLCIPHER_KEY must be one 64-hex SQLCipher key", file=sys.stderr)
        return 2

    if rel:
        manifest = {"keys": {rel: key.lower()}}
    else:
        manifest = {"__default_key": key.lower()}

    path = pathlib.Path(out_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    path.chmod(0o600)
    print(f"wrote manifest: {path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
