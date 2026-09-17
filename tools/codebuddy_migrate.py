#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""CodeBuddy -> WorkBuddy2API 账号凭证迁移工具。

从 koazy0/codebuddy2api 的账号库（SQLite / MySQL / PostgreSQL）只读导出凭证，
转换为 Sliverkiss/workbuddy2api 的 auths/workbuddy-<uid>.json 格式。

用法::

  # 1. 导出（直连数据库，只读打开）
  python3 codebuddy_migrate.py export --sqlite /path/gateway.db --out raw.json
  python3 codebuddy_migrate.py export --mysql  "mysql://user:pass@127.0.0.1:3306/codebuddy_gateway" --out raw.json
  python3 codebuddy_migrate.py export --pgsql  "postgresql://user:pass@127.0.0.1:5432/codebuddy_gateway" --out raw.json

  # 2. 转换（生成 workbuddy2api 的 auth 文件）
  python3 codebuddy_migrate.py convert --in raw.json --out-dir ./auths

  # 3. 校验（可选，顺带核对网关是否真的加载到）
  python3 codebuddy_migrate.py verify --auth-dir ./auths \
      --status-url http://127.0.0.1:7863/status --api-key sk-xxx

为什么必须直连数据库：koazy0 的 HTTP 管理接口在返回账号时对 jwt / refresh_token
调用 MaskToken() 掩码（api/handler/admin/account.go 的 publicAccount），
从 API 拿不到可用凭据，只能读库。
"""

import argparse
import base64
import json
import os
import re
import sys
from datetime import datetime, timezone

ACCOUNTS_TABLE = "accounts"

UUID_RE = re.compile(
    r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-"
    r"[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"
)
# CN 账号的 username 常见形态就是手机号，作为 realm 回落的辅助信号。
PHONE_RE = re.compile(r"^1[3-9]\d{9}$")

UID_KEYS = ("uid", "userId", "user_id", "sub")
NICK_KEYS = ("nickname", "name", "Nickname", "displayName", "preferred_username")
JWT_KEYS = ("jwt", "accessToken", "access_token", "AccessToken", "token")
RT_KEYS = ("refresh_token", "refreshToken", "RefreshToken")
SESSION_KEYS = ("session_cookie", "sessionCookie", "session", "cookie")


def info(msg):
    print(msg)


def warn(msg):
    print("WARN: " + msg, file=sys.stderr)


def die(msg, code=1):
    print("ERROR: " + msg, file=sys.stderr)
    sys.exit(code)


# --------------------------------------------------------------------------
# JWT / 字段解析
# --------------------------------------------------------------------------

def b64url_decode(seg):
    seg = seg.strip()
    seg += "=" * (-len(seg) % 4)
    return base64.urlsafe_b64decode(seg)


def parse_jwt(token):
    """解出 JWT payload；不是标准三段 JWT 或解不开时返回空 dict（不抛错）。"""
    if not token or str(token).count(".") != 2:
        return {}
    try:
        payload = json.loads(b64url_decode(str(token).split(".")[1]))
        return payload if isinstance(payload, dict) else {}
    except Exception:
        return {}


def to_unix(value):
    """把各种时间表示归一为 Unix 秒；不可解析返回 0。"""
    if value is None:
        return 0
    if isinstance(value, bool):
        return 0
    if isinstance(value, (int, float)):
        n = int(value)
        # 毫秒时间戳（13 位）折算成秒
        return n // 1000 if n > 10 ** 11 else n
    s = str(value).strip()
    if not s:
        return 0
    try:
        if s.endswith("Z"):
            s = s[:-1] + "+00:00"
        dt = datetime.fromisoformat(s)
        if dt.tzinfo is None:
            dt = dt.replace(tzinfo=timezone.utc)
        return int(dt.timestamp())
    except Exception:
        return 0


def is_uuid(s):
    return bool(s) and bool(UUID_RE.match(str(s).strip()))


def pick_uid(payload, rec):
    """UID 优先级：库列 user_id > JWT.sub > remark(UUID) > payload.uid。

    只接受 UUID 形态（与 koazy0 的 normalizeImportedUID 同口径）：上游成长中心
    按 userId 聚合事件，把手机号/昵称当 uid 上报会被静默丢弃。
    """
    for cand in (rec.get("user_id"), payload.get("sub"), rec.get("remark")):
        if is_uuid(cand):
            return str(cand).strip()
    return ""


def detect_realm(payload, username=""):
    """判定 realm。workbuddy2api 的 Realm() 只认 .workbuddy.ai 域为 global。

    这里按 JWT 的 iss 判定，比 domain 列可靠（koazy0 没存 domain）。
    """
    iss = str(payload.get("iss", "")).lower()
    if "workbuddy.ai" in iss:
        return "global"
    if "codebuddy.cn" in iss or "tencent.com" in iss:
        return "cn"
    if username and PHONE_RE.match(str(username).strip()):
        return "cn"
    return "cn"


def first_str(rec, keys):
    for k in keys:
        v = rec.get(k)
        if v is None:
            continue
        s = str(v).strip()
        if s:
            return s
    return ""


# --------------------------------------------------------------------------
# 数据源读取（全部只读）
# --------------------------------------------------------------------------

def load_sqlite(path):
    import sqlite3

    if not os.path.exists(path):
        die("SQLite 文件不存在: %s" % path)
    con = sqlite3.connect("file:%s?mode=ro" % path, uri=True)
    con.row_factory = sqlite3.Row
    try:
        cur = con.execute(
            "SELECT * FROM %s WHERE deleted_at IS NULL" % ACCOUNTS_TABLE
        )
        return [dict(r) for r in cur.fetchall()]
    except sqlite3.Error as e:
        die("读取 SQLite 失败: %s" % e)
    finally:
        con.close()


def _parse_dsn(dsn, default_scheme, default_port):
    from urllib.parse import urlparse, unquote

    if "://" not in dsn:
        dsn = "%s://%s" % (default_scheme, dsn)
    u = urlparse(dsn)
    return {
        "host": u.hostname or "127.0.0.1",
        "port": u.port or default_port,
        "user": unquote(u.username) if u.username else None,
        "password": unquote(u.password) if u.password else "",
        "dbname": (u.path or "").lstrip("/") or None,
    }


def load_mysql(dsn):
    try:
        import pymysql
    except ImportError:
        die("缺少 pymysql：python3 -m pip install --break-system-packages pymysql")
    c = _parse_dsn(dsn, "mysql", 3306)
    try:
        con = pymysql.connect(
            host=c["host"],
            port=c["port"],
            user=c["user"] or "root",
            password=c["password"],
            database=c["dbname"],
            charset="utf8mb4",
            connect_timeout=10,
            cursorclass=pymysql.cursors.DictCursor,
        )
    except Exception as e:
        die("连接 MySQL 失败 (%s:%s/%s): %s" % (c["host"], c["port"], c["dbname"], e))
    try:
        with con.cursor() as cur:
            cur.execute(
                "SELECT * FROM %s WHERE deleted_at IS NULL" % ACCOUNTS_TABLE
            )
            return [dict(r) for r in cur.fetchall()]
    except Exception as e:
        die("读取 MySQL 失败: %s" % e)
    finally:
        con.close()


def load_pgsql(dsn):
    try:
        import psycopg2
        import psycopg2.extras
    except ImportError:
        die("缺少 psycopg2：python3 -m pip install --break-system-packages psycopg2-binary")
    c = _parse_dsn(dsn, "postgresql", 5432)
    try:
        con = psycopg2.connect(
            host=c["host"],
            port=c["port"],
            user=c["user"] or "postgres",
            password=c["password"],
            dbname=c["dbname"] or "postgres",
            connect_timeout=10,
        )
    except Exception as e:
        die("连接 PostgreSQL 失败 (%s:%s/%s): %s" % (c["host"], c["port"], c["dbname"], e))
    try:
        with con.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
            cur.execute(
                "SELECT * FROM %s WHERE deleted_at IS NULL" % ACCOUNTS_TABLE
            )
            return [dict(r) for r in cur.fetchall()]
    except Exception as e:
        die("读取 PostgreSQL 失败: %s" % e)
    finally:
        con.close()


# --------------------------------------------------------------------------
# export
# --------------------------------------------------------------------------

def normalize_record(rec):
    """把一条库记录转成迁移用的中间结构；无可用 accessToken 时返回 None。"""
    jwt = first_str(rec, JWT_KEYS)
    if not jwt:
        return None
    jwt = jwt[7:].strip() if jwt.lower().startswith("bearer ") else jwt
    if not jwt:
        return None

    payload = parse_jwt(jwt)
    username = first_str(rec, ("username", "preferred_username")) or str(
        payload.get("preferred_username", "")
    )
    nickname = first_str(rec, NICK_KEYS) or str(payload.get("nickname", ""))
    uid = pick_uid(payload, rec)

    # expiresAt 优先取 JWT 的 exp（真实凭据），回落库里的 jwt_expires_at 列。
    access_exp = to_unix(payload.get("exp")) or to_unix(rec.get("jwt_expires_at"))
    refresh_exp = to_unix(rec.get("refresh_expires_at"))

    return {
        "uid": uid,
        "nickname": nickname,
        "username": username,
        "jwt": jwt,
        "refresh_token": first_str(rec, RT_KEYS),
        "session_cookie": first_str(rec, SESSION_KEYS),
        "realm": detect_realm(payload, username),
        "access_expires_at": access_exp,
        "refresh_expires_at": refresh_exp,
        "status": str(rec.get("status") or "enabled").strip().lower(),
        "weight": rec.get("weight"),
        "remark": str(rec.get("remark") or ""),
        "credit_remain": rec.get("credit_remain"),
        "credit_used": rec.get("credit_used"),
    }


def cmd_export(args):
    sources = [x for x in (args.sqlite, args.mysql, args.pgsql) if x]
    if len(sources) != 1:
        die("export 需要且只能指定一个数据源：--sqlite | --mysql | --pgsql")

    if args.sqlite:
        rows = load_sqlite(args.sqlite)
        src_desc = "sqlite:%s" % args.sqlite
    elif args.mysql:
        rows = load_mysql(args.mysql)
        src_desc = "mysql:%s" % _safe_dsn(args.mysql)
    else:
        rows = load_pgsql(args.pgsql)
        src_desc = "pgsql:%s" % _safe_dsn(args.pgsql)

    info("数据源 %s，读到 %d 条账号记录" % (src_desc, len(rows)))

    accounts, skipped = [], 0
    for r in rows:
        rec = normalize_record(r)
        if rec is None:
            skipped += 1
            continue
        if rec["status"] != "enabled" and not args.include_disabled:
            skipped += 1
            continue
        accounts.append(rec)

    if skipped:
        info("跳过 %d 条（无凭据或非 enabled；用 --include-disabled 保留全部）" % skipped)
    if not accounts:
        die("没有可导出的账号")

    doc = {
        "source": src_desc,
        "exported_at": datetime.now(timezone.utc).isoformat(),
        "count": len(accounts),
        "accounts": accounts,
    }
    _write_json(args.out, doc, secret=False)

    info("已导出 %d 个账号 -> %s" % (len(accounts), args.out))
    _print_table(accounts)
    if not args.quiet:
        info("")
        info("下一步：python3 %s convert --in %s --out-dir ./auths"
             % (os.path.basename(__file__), args.out))


def _safe_dsn(dsn):
    return re.sub(r"://([^:/@]+):[^@]*@", r"://\1:***@", dsn)


def safe_filename_component(s):
    """把任意字符串压成安全的文件名片段。

    文件名回退会用到 username / nickname，这些值来自库或第三方导出文件，
    可能含 / .. 等路径字符；不 sanitize 会让 os.path.join 逃出 out-dir。
    """
    s = re.sub(r"[^0-9A-Za-z._@-]", "_", str(s or "").strip())
    s = s.strip("._") or ""
    return s[:64]


def cmd_convert(args):
    if not os.path.exists(args.inp):
        die("输入文件不存在: %s" % args.inp)
    with open(args.inp, "r", encoding="utf-8") as f:
        data = json.load(f)

    records = collect_records(data)
    if not records:
        die("没从 %s 中解析出任何账号" % args.inp)

    out_dir = args.out_dir
    if not args.dry_run:
        os.makedirs(out_dir, exist_ok=True)

    written, seen_uid, skipped, disabled_skipped = [], {}, 0, 0
    for rec in records:
        item = normalize_record(rec)
        if item is None:
            skipped += 1
            continue

        if item["status"] != "enabled" and not args.include_disabled:
            disabled_skipped += 1
            continue

        uid = item["uid"]
        if not uid:
            # 没有 UUID 也必须落盘：workbuddy2api 的 LoadDir 允许 uid 为空
            # （仅影响日志标签与去重告警），凭证本身照常可用。
            uid = item["username"] or item["nickname"] or ""

        key = uid or item["jwt"][-16:]
        if key in seen_uid:
            warn("重复账号（uid/尾码=%s），沿用首个文件: %s" % (key, seen_uid[key]))
            skipped += 1
            continue
        seen_uid[key] = "(本次)"

        # 文件名只以 UUID 为准（最稳定且唯一）；否则用 sanitize 后的回退值，
        # 避免 username 里的路径字符或超长昵称产生非法文件名。
        if is_uuid(item["uid"]):
            stem = item["uid"]
        else:
            stem = safe_filename_component(uid) or "unnamed-%d" % (len(written) + 1)
        name = "workbuddy-%s.json" % stem
        path = os.path.join(out_dir, name)

        doc = {
            "auth": {
                "accessToken": item["jwt"],
                "refreshToken": item["refresh_token"],
                "expiresAt": item["access_expires_at"],
                # domain 留空：koazy0 库没有该列，workbuddy2api 在 domain 为空时
                # 改发 X-No-Department-Info: 1，是它自己定义的安全回落，
                # 比凭空编造一个域值强（编错域名会导致越权头的错误取值）。
                "domain": "",
                "realm": item["realm"],
            },
            "account": {
                "uid": item["uid"],
                "enterpriseId": "",
                "nickname": item["nickname"],
            },
        }

        if args.dry_run:
            info("[dry-run] 将写入 %s (realm=%s)" % (path, item["realm"]))
        else:
            _write_json(path, doc, secret=True)
        written.append((path, item, name))

    info("")
    info("生成 %d 个 auth 文件 -> %s%s" % (len(written), out_dir,
                                          "（dry-run，未落盘）" if args.dry_run else ""))
    if disabled_skipped:
        info("跳过 %d 条非 enabled 账号（用 --include-disabled 保留）" % disabled_skipped)
    if skipped:
        info("跳过 %d 条重复/无凭据记录" % skipped)

    _print_table([w[1] for w in written])
    if written and not args.dry_run:
        info("")
        info("重启 workbuddy2api 使账号生效：")
        info("  pkill -f 'wb2api -config' ; nohup /opt/workbuddy2api/wb2api "
             "-config /opt/workbuddy2api/config.json > /opt/workbuddy2api/server.log 2>&1 &")


def _print_table(items):
    if not items:
        return
    info("")
    info("%-8s %-22s %-14s %-8s %-12s %s"
         % ("UID", "昵称", "realm", "状态", "有效期", "refresh"))
    info("-" * 92)
    now = int(datetime.now(timezone.utc).timestamp())
    for it in items:
        uid = it["uid"][:8] if it["uid"] else "-"
        nick = (it["nickname"] or it["username"] or "-")[:22]
        exp = it["access_expires_at"]
        if exp:
            days = (exp - now) // 86400
            exp_s = "%d天" % days if days >= 0 else "已过期"
        else:
            exp_s = "-"
        rt_s = "有" if it["refresh_token"] else "无"
        info("%-8s %-22s %-14s %-8s %-12s %s"
             % (uid, nick, it["realm"], it.get("status", "-"), exp_s, rt_s))


# --------------------------------------------------------------------------
# 输入归一化：兼容本工具导出格式 / koazy0 账号 JSON / 桌面端导出
# --------------------------------------------------------------------------

def collect_records(data):
    """递归抽取账号记录，兼容多种嵌套形态。"""
    out = []

    def walk(node, depth=0):
        if node is None or depth > 12:
            return
        if isinstance(node, list):
            for x in node:
                walk(x, depth + 1)
            return
        if not isinstance(node, dict):
            return

        if _looks_like_account(node):
            out.append(node)

        for v in node.values():
            if isinstance(v, (dict, list)):
                walk(v, depth + 1)

    walk(data)

    # 去重：同一 accessToken 只保留字段最全的一条
    best = {}
    for rec in out:
        jwt = first_str(rec, JWT_KEYS)
        if not jwt:
            continue
        cur = best.get(jwt)
        if cur is None or _richness(rec) > _richness(cur):
            best[jwt] = rec
    return list(best.values())


def _looks_like_account(obj):
    return bool(first_str(obj, JWT_KEYS))


def _richness(rec):
    return sum(1 for k in ("uid", "user_id", "nickname", "name", "username")
               if rec.get(k))


# --------------------------------------------------------------------------
# verify
# --------------------------------------------------------------------------

def cmd_verify(args):
    auth_dir = args.auth_dir
    if not os.path.isdir(auth_dir):
        die("目录不存在: %s" % auth_dir)

    files = sorted(
        f for f in os.listdir(auth_dir)
        if f.startswith("workbuddy") and f.endswith(".json")
    )
    if not files:
        die("%s 下没有 workbuddy*.json" % auth_dir)

    now = int(datetime.now(timezone.utc).timestamp())
    rows, ok, bad, uids = [], 0, 0, {}
    for fn in files:
        path = os.path.join(auth_dir, fn)
        try:
            with open(path, "r", encoding="utf-8") as f:
                raw = json.load(f)
        except Exception as e:
            warn("%s 解析失败: %s" % (fn, e))
            bad += 1
            continue

        auth = raw.get("auth") if isinstance(raw.get("auth"), dict) else raw
        acct = raw.get("account") if isinstance(raw.get("account"), dict) else {}
        at = str(auth.get("accessToken") or "")
        rt = str(auth.get("refreshToken") or "")
        uid = str(acct.get("uid") or "")
        realm = str(auth.get("realm") or "")
        exp = to_unix(auth.get("expiresAt"))
        payload = parse_jwt(at)

        errs = []
        if not at:
            errs.append("缺 accessToken")
        elif not payload:
            errs.append("accessToken 非 JWT")
        if exp and exp < now:
            errs.append("已过期")
        if not realm:
            errs.append("缺 realm（将按 domain 回落）")

        if uid:
            uids.setdefault(uid, []).append(fn)

        if errs:
            bad += 1
        else:
            ok += 1
        rows.append((fn, uid, realm, exp, rt, errs))

    info("校验 %s" % auth_dir)
    info("")
    info("%-34s %-10s %-8s %-10s %-6s %s"
         % ("文件", "UID", "realm", "有效期", "refresh", "问题"))
    info("-" * 96)
    for fn, uid, realm, exp, rt, errs in rows:
        exp_s = ("%d天" % ((exp - now) // 86400)) if exp else "-"
        info("%-34s %-10s %-8s %-10s %-6s %s"
             % (fn[:34], uid[:8] if uid else "-", realm or "-", exp_s,
                "有" if rt else "无", "；".join(errs) or "OK"))

    info("")
    info("合计 %d 个文件：%d 通过 / %d 有问题" % (len(rows), ok, bad))
    for uid, fns in uids.items():
        if len(fns) > 1:
            warn("uid %s 在多个文件中出现: %s" % (uid, ", ".join(fns)))

    if args.status_url:
        _check_gateway(args.status_url, args.api_key, len(rows))


def _check_gateway(url, api_key, local_count):
    try:
        from urllib.request import Request, urlopen
    except ImportError:
        return
    try:
        req = Request(url)
        if api_key:
            req.add_header("Authorization", "Bearer " + api_key)
        with urlopen(req, timeout=8) as resp:
            data = json.loads(resp.read().decode("utf-8"))
    except Exception as e:
        warn("查询网关 %s 失败: %s" % (url, e))
        return

    total = data.get("total")
    healthy = data.get("healthy")
    info("")
    info("网关 %s: total=%s healthy=%s disabled=%s sticky=%s"
         % (url, total, healthy, data.get("disabled"), data.get("sticky_sessions")))
    if isinstance(total, int) and total != local_count:
        warn("网关账号数 %s 与本地 auth 文件数 %s 不一致——确认网关指向了同一 auth 目录，"
             "并且已在写入后重启" % (total, local_count))


# --------------------------------------------------------------------------
# 写文件（原子 + 0600）
# --------------------------------------------------------------------------

def _write_json(path, doc, secret):
    parent = os.path.dirname(os.path.abspath(path))
    if parent:
        os.makedirs(parent, exist_ok=True)
    tmp = path + ".tmp"
    mode = 0o600 if secret else 0o644

    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, mode)
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(doc, f, ensure_ascii=False, indent=2)
        f.write("\n")
    os.chmod(tmp, mode)
    os.replace(tmp, path)


def main():
    p = argparse.ArgumentParser(
        description="CodeBuddy -> WorkBuddy2API 账号凭证迁移工具",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    sub = p.add_subparsers(dest="cmd", required=True)

    e = sub.add_parser("export", help="从 koazy0/codebuddy2api 数据库导出凭据（只读）")
    src = e.add_mutually_exclusive_group(required=True)
    src.add_argument("--sqlite", help="SQLite 文件路径，如 ./data/gateway.db")
    src.add_argument("--mysql", help='MySQL DSN，如 "mysql://user:pass@host:3306/codebuddy_gateway"')
    src.add_argument("--pgsql", help='PostgreSQL DSN，如 "postgresql://user:pass@host:5432/codebuddy_gateway"')
    e.add_argument("--out", default="raw_accounts.json", help="导出文件（默认 raw_accounts.json）")
    e.add_argument("--include-disabled", action="store_true", help="连非 enabled 账号一起导出")
    e.add_argument("--quiet", action="store_true")
    e.set_defaults(func=cmd_export)

    c = sub.add_parser("convert", help="把导出文件转成 workbuddy2api 的 auth 文件")
    c.add_argument("--in", dest="inp", required=True, help="export 的产物（也兼容 koazy0 的账号 JSON）")
    c.add_argument("--out-dir", default="./auths", help="auth 文件输出目录（默认 ./auths）")
    c.add_argument("--dry-run", action="store_true", help="只打印不落盘")
    c.add_argument("--include-disabled", action="store_true",
                   help="连非 enabled 账号一起转换（默认只转 enabled）")
    c.set_defaults(func=cmd_convert)

    v = sub.add_parser("verify", help="校验生成的 auth 文件")
    v.add_argument("--auth-dir", default="./auths")
    v.add_argument("--status-url", default="", help="可选：网关 /status，核对是否已加载")
    v.add_argument("--api-key", default="", help="网关 api_key")
    v.set_defaults(func=cmd_verify)

    args = p.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
