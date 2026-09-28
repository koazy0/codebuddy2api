#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""实时盘点账号余额，从余额升序第 20~50 名中筛余额为 0 的账号，随机抽 5 个导出 JSON。"""
import json
import os
import random
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor

AUTHS = "/opt/workbuddy2api/auths"
BASE = "https://www.codebuddy.cn"
UA = "WorkBuddy/5.5.6"
OUT = "/tmp/zero_bal_5.json"


def load_accounts():
    out = []
    for fn in sorted(os.listdir(AUTHS)):
        if not (fn.startswith("workbuddy") and fn.endswith(".json")):
            continue
        try:
            d = json.load(open(os.path.join(AUTHS, fn), encoding="utf-8"))
        except Exception:
            continue
        au = d.get("auth") or {}
        ac = d.get("account") or {}
        tok = (au.get("accessToken") or "").strip()
        uid = (ac.get("uid") or "").strip()
        if tok and uid:
            out.append({"token": tok, "uid": uid, "uid8": uid[:8],
                        "nick": ac.get("nickname") or "", "file": fn})
    return out


def get_bal(a):
    req = urllib.request.Request(
        BASE + "/portal/activity/school/config", method="GET",
        headers={"Authorization": "Bearer " + a["token"],
                 "Accept": "application/json", "User-Agent": UA,
                 "Origin": BASE, "Referer": BASE + "/"})
    try:
        with urllib.request.urlopen(req, timeout=25) as r:
            j = json.loads(r.read().decode())
            c = (j.get("data") or {}).get("chance") or {}
            return int(c.get("balance") or 0), True
    except urllib.error.HTTPError as e:
        if e.code in (401, 403):
            return -1, False  # token 失效
        return -2, False
    except Exception:
        return -2, False


def main():
    accts = load_accounts()
    print("账号总数: %d，正在实时查询余额..." % len(accts))
    with ThreadPoolExecutor(max_workers=8) as ex:
        for a, (bal, ok) in zip(accts, ex.map(get_bal, accts)):
            a["bal"] = bal
            a["ok"] = ok

    ok_accts = [a for a in accts if a["ok"]]
    ok_accts.sort(key=lambda x: (x["bal"], x["uid"]))
    print("有效查询: %d | 查询失败/失效: %d" % (len(ok_accts), len(accts) - len(ok_accts)))
    print("余额分布: " + ", ".join(
        "bal=%d x%d" % (b, sum(1 for a in ok_accts if a["bal"] == b))
        for b in sorted({a["bal"] for a in ok_accts})))

    mid = ok_accts[19:50]  # 第 20~50 名
    zeros = [a for a in mid if a["bal"] == 0]
    print("中间段(20~50名)共 %d 个，其中余额为 0 的: %d 个" % (len(mid), len(zeros)))
    if not zeros:
        print("中间段没有余额为 0 的账号，退出")
        return

    random.seed()
    picked = random.sample(zeros, min(5, len(zeros)))
    picked.sort(key=lambda x: x["uid"])

    export = [{"uid": a["uid"], "uid8": a["uid8"], "nickname": a["nick"],
               "balance": a["bal"], "accessToken": a["token"],
               "source_file": os.path.join(AUTHS, a["file"])} for a in picked]
    with open(OUT, "w", encoding="utf-8") as f:
        json.dump(export, f, ensure_ascii=False, indent=2)

    print("\n随机抽中 %d 个（余额升序排名 20~50 内，额度=0）:" % len(picked))
    for a in picked:
        rank = ok_accts.index(a) + 1
        print("  #%d %s %-16s bal=%d" % (rank, a["uid8"], a["nick"][:16], a["bal"]))
    print("已导出 -> %s" % OUT)


if __name__ == "__main__":
    main()
