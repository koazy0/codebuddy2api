#!/usr/bin/env python3
"""Import CodeBuddy accounts into the gateway. This script never refreshes tokens."""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


def pick(*values: Any) -> str:
    for value in values:
        if isinstance(value, str) and value.strip():
            return value.strip()
    return ""


def dict_get(obj: Any, *keys: str) -> Any:
    if not isinstance(obj, dict):
        return None
    cur: Any = obj
    for key in keys:
        if not isinstance(cur, dict):
            return None
        cur = cur.get(key)
    return cur


def extract_one(obj: Any) -> dict[str, str] | None:
    if not isinstance(obj, dict):
        return None
    auth = obj.get("auth") if isinstance(obj.get("auth"), dict) else {}
    account = obj.get("account") if isinstance(obj.get("account"), dict) else {}
    jwt = pick(
        obj.get("jwt"),
        obj.get("access_token"),
        obj.get("accessToken"),
        auth.get("accessToken"),
        auth.get("access_token"),
    )
    refresh = pick(
        obj.get("refresh_token"),
        obj.get("refreshToken"),
        auth.get("refreshToken"),
        auth.get("refresh_token"),
    )
    if not jwt:
        return None
    name = pick(obj.get("name"), obj.get("nickname"), account.get("nickname"))
    uid = pick(obj.get("uid"), account.get("uid"), obj.get("username"))
    session = pick(
        obj.get("session_cookie"),
        obj.get("sessionCookie"),
        obj.get("session"),
        obj.get("cookie"),
    )
    return {
        "name": name or uid or "imported",
        "jwt": jwt,
        "refresh_token": refresh,
        "session_cookie": session,
        "remark": uid,
    }


def extract_many(obj: Any) -> list[dict[str, str]]:
    found: list[dict[str, str]] = []
    seen: set[str] = set()

    def add(item: dict[str, str] | None) -> None:
        if not item:
            return
        token = item["jwt"]
        if token in seen:
            return
        seen.add(token)
        found.append(item)

    if isinstance(obj, list):
        for item in obj:
            for one in extract_many(item):
                add(one)
        return found

    if not isinstance(obj, dict):
        return found

    nested_keys = ("accounts", "allAccounts", "data", "items")
    for key in nested_keys:
        child = obj.get(key)
        if isinstance(child, list):
            for item in child:
                add(extract_one(item))
                if isinstance(item, dict):
                    add(extract_one({"auth": item.get("auth"), "account": item.get("account"), **item}))
    add(extract_one(obj))
    return found


def load_path(path: Path) -> list[dict[str, str]]:
    accounts: list[dict[str, str]] = []
    if path.is_dir():
        files = sorted(path.glob("*.json"))
        if not files:
            raise SystemExit(f"no json files in {path}")
        for file in files:
            accounts.extend(load_path(file))
        return dedupe(accounts)
    try:
        data = json.loads(path.read_text())
    except Exception as exc:
        raise SystemExit(f"failed to read {path}: {exc}") from exc
    extracted = extract_many(data)
    if not extracted:
        raise SystemExit(f"no jwt/accessToken found in {path}")
    return extracted


def dedupe(accounts: list[dict[str, str]]) -> list[dict[str, str]]:
    seen: set[str] = set()
    out: list[dict[str, str]] = []
    for item in accounts:
        if item["jwt"] in seen:
            continue
        seen.add(item["jwt"])
        out.append(item)
    return out


def post_json(url: str, payload: dict[str, Any], admin_key: str) -> dict[str, Any]:
    body = json.dumps(payload).encode()
    req = urllib.request.Request(
        url,
        data=body,
        method="POST",
        headers={
            "Authorization": f"Bearer {admin_key}",
            "Content-Type": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read()
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", "replace")
        raise SystemExit(f"import failed HTTP {exc.code}: {detail}") from exc
    except Exception as exc:
        raise SystemExit(f"import failed: {exc}") from exc
    if not raw:
        return {}
    return json.loads(raw.decode())


def mask(token: str) -> str:
    token = token.strip()
    if len(token) <= 16:
        return "****"
    return token[:8] + "..." + token[-6:]


def main() -> None:
    parser = argparse.ArgumentParser(description="Import accounts into codebuddy2api. Never refreshes tokens.")
    parser.add_argument("path", help="json file or directory")
    parser.add_argument("--gateway", default=os.environ.get("GATEWAY_URL", "http://127.0.0.1:8088"))
    parser.add_argument("--admin-key", default=os.environ.get("ADMIN_KEY", "sk-admin-change-me"))
    parser.add_argument("--dry-run", action="store_true", help="parse only, do not call the gateway")
    args = parser.parse_args()

    path = Path(args.path).expanduser()
    if not path.exists():
        raise SystemExit(f"path not found: {path}")

    accounts = dedupe(load_path(path))
    print(f"parsed {len(accounts)} account(s) from {path}")
    for item in accounts:
        print(f"- {item['name']} jwt={mask(item['jwt'])} refresh={mask(item['refresh_token'])}")

    if args.dry_run:
        print("dry-run: skip upload")
        return

    url = args.gateway.rstrip("/") + "/admin/accounts/import"
    result = post_json(url, {"accounts": accounts}, args.admin_key)
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
