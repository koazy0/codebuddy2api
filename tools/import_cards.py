#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""import_cards.py —— 把卡密批量导入 koazy0/codebuddy2api 的账号库。

零外部依赖：只用 Python 标准库（sqlite3 内建；MySQL 走可选 pymysql）。
在服务器上直接跑，不需要装 Go、不需要启动网关。

支持三种数据源（与 koazy0 的 system.db 配置对应）：
  --db sqlite   默认。自动定位 data/gateway.db
  --db mysql    需要 pymysql：pip install pymysql
  --db pgsql    需要 psycopg2：pip install psycopg2-binary

输入格式（三种都自动识别，无需手工转换）：
  1. 卡密 txt：每行 `第N张<TAB>accessToken----refreshToken`
  2. 本项目导出的 JSON：{"accounts":[{"jwt":..., "refresh_token":..., ...}]}
  3. 目录：递归读取目录下所有 *.txt / *.json

用法::

  # 1) 先干跑，只解析不写库（强烈建议先跑这个）
  python3 import_cards.py --input cards_50.txt --db sqlite --dry-run

  # 2) 确认无误后真写库
  python3 import_cards.py --input cards_50.txt --db sqlite

  # 3) 指定数据库路径 / MySQL
  python3 import_cards.py --input cards_50.txt --sqlite-path /root/glm/codebuddy2api/data/gateway.db
  python3 import_cards.py --input cards_50.txt --db mysql \
      --dsn "mysql://user:pass@127.0.0.1:3306/codebuddy_gateway"

  # 4) 导入后回读校验
  python3 import_cards.py --input cards_50.txt --db sqlite --verify

设计要点：
  - **幂等**：以 JWT 为唯一键；已存在的账号默认跳过（--update 可改为刷新其 token）。
  - **先备份**：写 SQLite 前自动复制一份 gateway.db.bak.<时间戳>。
  - **只插不删**：不做任何 DELETE / UPDATE 之外的破坏性操作。
  - uid 从 JWT 的 sub 解析（与 koazy0 的 UID() 同口径），带 user_id 列时一并落库。
"""

import argparse
import base64
import json
import os
import re
import shutil
import sqlite3
import sys
import time
from datetime import datetime, timezone

TABLE = "accounts"
UUID_RE = re.compile(
    r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-"
    r"[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"
)
CARD_RE = re.compile(r"^第\s*(\d+)\s*张\s*[\t :]*([A-Za-z0-9_\-\.]+----[A-Za-z0-9_\-\.]+)\s*$")
CARD_RE_LOOSE = re.compile(r"^([A-Za-z0-9_\-]{40,})----([A-Za-z0-9_\-]{40,})\s*$")


def info(m):
    print(m)


def warn(m):
    print("WARN: " + m, file=sys.stderr)


def die(m, code=1):
    print("ERROR: " + m, file=sys.stderr)
    sys.exit(code)


# --------------------------------------------------------------------------
# 解析
# --------------------------------------------------------------------------
def b64url(seg):
    seg += "=" * (-len(seg) % 4)
    return base64.urlsafe_b64decode(seg)


def jwt_payload(tok):
    if not tok or tok.count(".") != 2:
        return {}
    try:
        p = json.loads(b64url(tok.split(".")[1]))
        return p if isinstance(p, dict) else {}
    except Exception:
        return {}


def parse_cards_text(text):
    """从卡密文本抽取 [{access, refresh}]。"""
    out = []
    for line in text.splitlines():
        line = line.rstrip()
        if not line.strip():
            continue
        m = CARD_RE.match(line)
        if not m:
            m = CARD_RE_LOOSE.match(line.strip())
            if not m:
                continue
            pair = m.group(1) + "----" + m.group(2)
        else:
            pair = m.group(2)
        if "----" not in pair:
            continue
        at, rt = pair.split("----", 1)
        at, rt = at.strip(), rt.strip()
        if at.count(".") != 2:
            warn("跳过非 JWT 行: %s..." % at[:30])
            continue
        out.append({"access": at, "refresh": rt if rt.count(".") == 2 else ""})
    return out


def parse_json_accounts(obj):
    """递归抽取含 accessToken / jwt 的账号对象。"""
    found = []

    def walk(n, d=0):
        if n is None or d > 12:
            return
        if isinstance(n, list):
            for x in n:
                walk(x, d + 1)
            return
        if not isinstance(n, dict):
            return
        at = None
        for k in ("accessToken", "access_token", "jwt", "AccessToken", "token"):
            v = n.get(k)
            if isinstance(v, str) and v.strip().count(".") == 2:
                at = v.strip()
                break
        au = n.get("auth") if isinstance(n.get("auth"), dict) else None
        ac = n.get("account") if isinstance(n.get("account"), dict) else None
        if not at and au:
            for k in ("accessToken", "access_token", "jwt"):
                v = au.get(k)
                if isinstance(v, str) and v.strip().count(".") == 2:
                    at = v.strip()
                    break
        if at:
            rt = ""
            for src in (n, au or {}):
                for k in ("refreshToken", "refresh_token", "RefreshToken"):
                    v = src.get(k)
                    if isinstance(v, str) and v.strip():
                        rt = v.strip()
                        break
                if rt:
                    break
            uid = ""
            for src in (n, ac or {}, au or {}):
                for k in ("uid", "user_id", "userId", "sub"):
                    v = src.get(k)
                    if isinstance(v, str) and UUID_RE.match(v.strip()):
                        uid = v.strip()
                        break
                if uid:
                    break
            nick = ""
            for src in (n, ac or {}):
                for k in ("nickname", "name", "Name"):
                    v = src.get(k)
                    if isinstance(v, str) and v.strip():
                        nick = v.strip()
                        break
                if nick:
                    break
            found.append({"access": at, "refresh": rt, "uid": uid, "nickname": nick})
        for v in n.values():
            if isinstance(v, (dict, list)):
                walk(v, d + 1)

    walk(obj)
    # 去重（同一 access 只留一条）
    best = {}
    for r in found:
        best.setdefault(r["access"], r)
    return list(best.values())


def collect_input(path):
    """把输入（文件或目录）解析成统一记录列表。"""
    files = []
    if os.path.isdir(path):
        for root, _, fns in os.walk(path):
            for fn in fns:
                if fn.lower().endswith((".txt", ".json", ".csv")):
                    files.append(os.path.join(root, fn))
    elif os.path.isfile(path):
        files.append(path)
    else:
        die("输入不存在: %s" % path)

    if not files:
        die("目录下没有 .txt/.json/.csv: %s" % path)

    recs = []
    for f in sorted(files):
        try:
            with open(f, "r", encoding="utf-8", errors="replace") as fh:
                raw = fh.read()
        except Exception as e:
            warn("读取失败 %s: %s" % (f, e))
            continue
        got = []
        low = f.lower()
        if low.endswith(".json"):
            try:
                got = parse_json_accounts(json.loads(raw))
            except Exception as e:
                warn("JSON 解析失败 %s: %s" % (f, e))
        else:
            got = parse_cards_text(raw)
        info("  %-40s -> %d 条" % (os.path.basename(f)[:40], len(got)))
        recs.extend(got)

    # 全量去重
    uniq = {}
    for r in recs:
        uniq.setdefault(r["access"], r)
    return list(uniq.values())


def enrich(rec):
    """补齐 uid / nickname / 有效期，并推断 realm。"""
    p = jwt_payload(rec["access"])
    rp = jwt_payload(rec.get("refresh") or "")
    if not rec.get("uid"):
        s = p.get("sub", "")
        rec["uid"] = s if UUID_RE.match(str(s)) else ""
    if not rec.get("nickname"):
        rec["nickname"] = p.get("nickname", "") or p.get("preferred_username", "")
    rec["access_exp"] = p.get("exp") or 0
    rec["refresh_exp"] = rp.get("exp") or 0
    iss = str(p.get("iss", "")).lower()
    rec["realm"] = "global" if "workbuddy.ai" in iss else "cn"
    rec["username"] = p.get("preferred_username", "")
    return rec


# --------------------------------------------------------------------------
# 数据库
# --------------------------------------------------------------------------
def dsn_parse(dsn, scheme, port):
    from urllib.parse import urlparse, unquote
    if "://" not in dsn:
        dsn = "%s://%s" % (scheme, dsn)
    u = urlparse(dsn)
    return {
        "host": u.hostname or "127.0.0.1",
        "port": u.port or port,
        "user": unquote(u.username) if u.username else None,
        "password": unquote(u.password) if u.password else "",
        "dbname": (u.path or "").lstrip("/") or None,
    }


def sqlite_connect(path, for_write):
    if not os.path.exists(path):
        die("SQLite 文件不存在: %s" % path)
    if for_write:
        bak = "%s.bak.%s" % (path, time.strftime("%Y%m%d-%H%M%S"))
        try:
            shutil.copy2(path, bak)
            info("已备份原库 -> %s" % bak)
        except Exception as e:
            die("备份失败，已中止（避免无备份写库）: %s" % e)
    con = sqlite3.connect(path)
    con.row_factory = sqlite3.Row
    return con


def mysql_connect(dsn):
    try:
        import pymysql
    except ImportError:
        die("需要 pymysql：pip install pymysql")
    c = dsn_parse(dsn, "mysql", 3306)
    try:
        return pymysql.connect(host=c["host"], port=c["port"], user=c["user"] or "root",
                               password=c["password"], database=c["dbname"],
                               charset="utf8mb4", connect_timeout=10,
                               cursorclass=pymysql.cursors.DictCursor)
    except Exception as e:
        die("连接 MySQL 失败 (%s:%s/%s): %s" % (c["host"], c["port"], c["dbname"], e))


def pgsql_connect(dsn):
    try:
        import psycopg2
        import psycopg2.extras as ex
    except ImportError:
        die("需要 psycopg2：pip install psycopg2-binary")
    c = dsn_parse(dsn, "postgresql", 5432)
    try:
        con = psycopg2.connect(host=c["host"], port=c["port"], user=c["user"] or "postgres",
                               password=c["password"], dbname=c["dbname"] or "postgres",
                               connect_timeout=10)
        return con, ex
    except Exception as e:
        die("连接 PostgreSQL 失败 (%s:%s/%s): %s" % (c["host"], c["port"], c["dbname"], e))


def table_columns(con, db):
    """返回 accounts 表的列名集合（用于兼容没有 user_id 列的旧库）。"""
    try:
        if db == "sqlite":
            cur = con.execute("PRAGMA table_info(%s)" % TABLE)
            return {r[1] for r in cur.fetchall()}
        cur = con.cursor()
        if db == "mysql":
            cur.execute("SHOW COLUMNS FROM %s" % TABLE)
            return {r["Field"] for r in cur.fetchall()}
        cur.execute(
            "SELECT column_name FROM information_schema.columns WHERE table_name=%s",
            (TABLE,),
        )
        return {r[0] for r in cur.fetchall()}
    except Exception as e:
        die("读取表结构失败（表 %s 是否存在？）: %s" % (TABLE, e))


def find_existing(con, db, access_tokens, cols):
    """返回已存在的 JWT 集合（按 10 个一批查询，避免变量数上限）。"""
    if not access_tokens:
        return set()
    exist = set()
    if db == "sqlite":
        cur = con.cursor()
        for i in range(0, len(access_tokens), 10):
            chunk = access_tokens[i:i + 10]
            ph = ",".join("?" * len(chunk))
            try:
                cur.execute("SELECT jwt FROM %s WHERE jwt IN (%s)" % (TABLE, ph), chunk)
                exist.update(r[0] for r in cur.fetchall() if r[0])
            except Exception as e:
                die("查询已存在账号失败: %s" % e)
        return exist

    cur = con.cursor()
    for i in range(0, len(access_tokens), 10):
        chunk = access_tokens[i:i + 10]
        ph = ",".join(["%s"] * len(chunk))
        try:
            cur.execute("SELECT jwt FROM %s WHERE jwt IN (%s)" % (TABLE, ph), chunk)
            for r in cur.fetchall():
                v = r["jwt"] if isinstance(r, dict) else r[0]
                if v:
                    exist.add(v)
        except Exception as e:
            die("查询已存在账号失败: %s" % e)
    return exist


def insert_rows(con, db, rows, cols):
    """插入账号行。只带表里实际存在的列。"""
    if not rows:
        return 0

    want = ["name", "username", "jwt", "refresh_token", "session_cookie",
            "status", "weight", "user_id", "remark"]
    use = [c for c in want if c in cols]
    if "jwt" not in use:
        die("目标表缺少 jwt 列，表结构不符")

    now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M:%S")
    values = []
    for r in rows:
        item = {
            "name": r.get("nickname") or r.get("username") or "imported",
            "username": r.get("username") or "",
            "jwt": r["access"],
            "refresh_token": r.get("refresh") or "",
            "session_cookie": "",
            "status": "enabled",
            "weight": 1,
            "user_id": r.get("uid") or "",
            "remark": r.get("uid") or "",
        }
        values.append(tuple(item.get(c) for c in use))

    ph = ",".join(["%s"] * len(use))
    sql = "INSERT INTO %s (%s) VALUES (%s)" % (TABLE, ",".join(use), ph)
    if db == "sqlite":
        sql = sql.replace("%s", "?")

    cur = con.cursor()
    n = 0
    for v in values:
        try:
            cur.execute(sql, v)
            n += 1
        except Exception as e:
            warn("插入失败（可能唯一约束冲突）: %s" % str(e)[:120])
    con.commit()
    return n


def update_rows(con, db, rows, cols):
    """--update：按 jwt 匹配，刷新其 refresh_token / 状态。"""
    cur = con.cursor()
    n = 0
    upd = ["refresh_token = %s", "status = 'enabled'"]
    if "user_id" in cols:
        upd.append("user_id = %s")
    sql = "UPDATE %s SET %s WHERE jwt = %s" % (TABLE, ", ".join(upd), "%s")
    if db == "sqlite":
        sql = sql.replace("%s", "?")
    for r in rows:
        args = [r.get("refresh") or ""]
        if "user_id" in cols:
            args.append(r.get("uid") or "")
        args.append(r["access"])
        try:
            cur.execute(sql, args)
            n += cur.rowcount if cur.rowcount and cur.rowcount > 0 else 0
        except Exception as e:
            warn("更新失败: %s" % str(e)[:120])
    con.commit()
    return n


def verify(con, db, cols):
    """回读账号总数与前若干行做校验。"""
    cur = con.cursor()
    sql = "SELECT COUNT(*) AS n FROM %s" % TABLE
    if db == "sqlite":
        cur.execute(sql)
        total = cur.fetchone()[0]
    else:
        cur.execute(sql)
        r = cur.fetchone()
        total = r["n"] if isinstance(r, dict) else r[0]
    info("库内账号总数: %s" % total)

    if db == "sqlite":
        cur.execute(
            "SELECT name, username, substr(jwt,1,24) AS j, length(jwt) AS jl, "
            "length(refresh_token) AS rl, status FROM %s "
            "ORDER BY id DESC LIMIT 8" % TABLE
        )
        for r in cur.fetchall():
            info("  %-16s %-14s jwt=%s...(%s) rt=%s %s"
                 % ((r["name"] or "")[:16], (r["username"] or "")[:14],
                    r["j"], r["jl"], r["rl"], r["status"]))
    return total


# --------------------------------------------------------------------------
def main():
    ap = argparse.ArgumentParser(
        description="把卡密导入 koazy0/codebuddy2api 账号库（零依赖，幂等）",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    ap.add_argument("--input", required=True, help="卡密 txt / JSON / 目录")
    ap.add_argument("--db", choices=["sqlite", "mysql", "pgsql"], default="sqlite")
    ap.add_argument("--sqlite-path", default="", help="SQLite 路径（默认自动查找 data/gateway.db）")
    ap.add_argument("--dsn", default="", help="MySQL/PG 的 DSN")
    ap.add_argument("--dry-run", action="store_true", help="只解析不写库（建议先跑）")
    ap.add_argument("--update", action="store_true", help="已存在的账号改为刷新 token（默认跳过）")
    ap.add_argument("--verify", action="store_true", help="写完后回读校验")
    a = ap.parse_args()

    info("== 1. 解析输入 ==")
    recs = [enrich(r) for r in collect_input(a.input)]
    if not recs:
        die("没解析到任何卡密（检查格式是否为 access----refresh 或用第N张 前缀）")

    seen, uniq = set(), []
    for r in recs:
        if r["access"] in seen:
            continue
        seen.add(r["access"])
        uniq.append(r)
    recs = uniq

    info("")
    info("解析到 %d 个账号：" % len(recs))
    info("%-4s %-10s %-20s %-14s %-8s %s"
         % ("#", "uid", "昵称", "用户名", "realm", "refresh"))
    info("-" * 76)
    for i, r in enumerate(recs, 1):
        info("%-4d %-10s %-20s %-14s %-8s %s"
             % (i, (r["uid"] or "-")[:10], (r["nickname"] or "-")[:20],
                (r["username"] or "-")[:14], r["realm"],
                "有" if r.get("refresh") else "无"))
    no_rt = sum(1 for r in recs if not r.get("refresh"))
    no_uid = sum(1 for r in recs if not r.get("uid"))
    if no_rt:
        warn("%d 个账号没有 refresh_token（access 过期后需重新登录）" % no_rt)
    if no_uid:
        warn("%d 个账号 JWT 里没有 UUID 形态的 sub" % no_uid)

    if a.dry_run:
        info("")
        info("--dry-run：未写入任何数据。去掉 --dry-run 即真写库。")
        return

    info("")
    info("== 2. 连接数据库 ==")
    if a.db == "sqlite":
        path = a.sqlite_path
        if not path:
            for cand in ("data/gateway.db", "./gateway.db",
                         "/root/glm/codebuddy2api/data/gateway.db"):
                if os.path.exists(cand):
                    path = cand
                    break
        if not path:
            die("找不到 SQLite 文件，用 --sqlite-path 指定")
        info("SQLite: %s" % os.path.abspath(path))
        con = sqlite_connect(path, for_write=True)
        ex_con = None
    elif a.db == "mysql":
        if not a.dsn:
            die("--db mysql 需要 --dsn")
        con = mysql_connect(a.dsn)
        ex_con = None
    else:
        if not a.dsn:
            die("--db pgsql 需要 --dsn")
        con, ex_con = pgsql_connect(a.dsn)

    try:
        cols = table_columns(con, a.db)
        info("表 %s 可见列: %d 个%s"
             % (TABLE, len(cols), "" if "user_id" in cols else "（无 user_id，将不写该列）"))

        info("")
        info("== 3. 查重 ==")
        exist = find_existing(con, a.db, [r["access"] for r in recs], cols)
        new = [r for r in recs if r["access"] not in exist]
        old = [r for r in recs if r["access"] in exist]
        info("已存在: %d | 全新: %d" % (len(old), len(new)))

        info("")
        info("== 4. 写入 ==")
        ins = insert_rows(con, a.db, new, cols) if new else 0
        info("插入 %d 行" % ins)
        upd = 0
        if old and a.update:
            upd = update_rows(con, a.db, old, cols)
            info("更新 %d 行（--update 已生效）" % upd)
        elif old:
            info("跳过 %d 行已存在账号（要刷新它们用 --update）" % len(old))

        if a.verify:
            info("")
            info("== 5. 回读校验 ==")
            verify(con, a.db, cols)

        info("")
        info("完成：新增 %d，更新 %d，跳过 %d" % (ins, upd, len(old) - upd if a.update else len(old)))
    finally:
        try:
            con.close()
        except Exception:
            pass


if __name__ == "__main__":
    main()
