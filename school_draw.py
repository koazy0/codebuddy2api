#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""school_draw —— 开学季转盘抽奖（抽到空为止，带重试与容错）。

端点：
  GET  /portal/activity/school/config       → chance.balance（次数余额）
  POST /portal/activity/school/wheel/draw   → {draw_uuid}，返回 prize_code/credit_amount

奖品：实物券（瑞幸/肯德基冰淇淋/肯德基OK餐/酷狗）+ 积分（6/66）
用法:
  python3 school_draw.py            # 只读盘点
  python3 school_draw.py --yes      # 真实抽奖（抽到余额 0）
  python3 school_draw.py --yes --max 1   # 每号最多抽 N 次（调试）
"""

import argparse
import json
import os
import threading
import time
import urllib.error
import urllib.request
import uuid
from collections import Counter
from concurrent.futures import ThreadPoolExecutor, as_completed

AUTHS = os.environ.get("SD_AUTHS", "/opt/workbuddy2api/auths")
UA = "WorkBuddy/5.5.6"
BASE = "https://www.codebuddy.cn"
SCHOOL = BASE + "/portal/activity/school"
LOCK = threading.Lock()
ST = Counter()
WON = []       # 中奖实物券
DETAIL = []

PRIZE_LABEL = {
    "school_voucher_luckin": "瑞幸咖啡15元券",
    "school_voucher_kfc_ok": "肯德基OK餐券",
    "school_voucher_kfc_ice": "肯德基冰淇淋券",
    "school_voucher_kugou": "酷狗会员月卡券",
    "school_credit_6": "6积分",
    "school_credit_66": "66积分",
}


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


def http(a, method, path, body=None, timeout=25):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, method=method,
                                 headers={"Authorization": "Bearer " + a["token"],
                                          "Content-Type": "application/json",
                                          "Accept": "application/json",
                                          "User-Agent": UA,
                                          "Origin": BASE, "Referer": BASE + "/"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read().decode())
        except Exception:
            return e.code, {}
    except Exception:
        return -1, {}


def get_chance(a):
    for _ in range(3):
        st, j = http(a, "GET", "/portal/activity/school/config")
        if st == 200:
            c = (j.get("data") or {})
            return c, (c.get("chance") or {})
        time.sleep(2)
    return None, {}


def draw(a):
    for _ in range(3):
        st, j = http(a, "POST", "/portal/activity/school/wheel/draw",
                     {"draw_uuid": str(uuid.uuid4())})
        if st == 200:
            return True, (j.get("data") or {}), "ok"
        if st == 409 or (j.get("code") == 40900):
            return False, {}, "nochance"
        time.sleep(2)
    return False, {}, "err"


def work(a, yes, cap):
    uid8 = a["uid8"]
    cfg, ch = get_chance(a)
    if cfg is None:
        with LOCK:
            ST["cfg_err"] += 1
        return
    bal = int(ch.get("balance") or 0)
    if not cfg.get("in_period"):
        with LOCK:
            ST["out_period"] += 1
        return
    rec = {"uid8": uid8, "nick": a["nick"], "bal": bal, "drawn": 0, "prizes": []}
    if not yes or bal <= 0:
        with LOCK:
            if bal > 0:
                ST["pending"] += 1
            ST["bal_total"] += bal
            if bal > 0 and bal > ST["bal_max"]:
                ST["bal_max"] = bal
            DETAIL.append(rec)
        return
    n = min(bal, cap) if cap > 0 else bal
    for _ in range(n):
        ok, d, msg = draw(a)
        if not ok:
            if msg == "nochance":
                with LOCK:
                    ST["drained"] += 1
            else:
                with LOCK:
                    ST["draw_err"] += 1
            break
        pc = d.get("prize_code") or ""
        ca = int(d.get("credit_amount") or 0)
        label = PRIZE_LABEL.get(pc, pc)
        rec["drawn"] += 1
        rec["prizes"].append({"code": pc, "label": label, "credit": ca})
        with LOCK:
            ST["drawn"] += 1
            if pc.startswith("school_voucher"):
                ST["voucher"] += 1
                if pc == "school_voucher_luckin":
                    ST["luckin"] += 1
                elif pc == "school_voucher_kfc_ice":
                    ST["kfc_ice"] += 1
                elif pc == "school_voucher_kfc_ok":
                    ST["kfc_ok"] += 1
                elif pc == "school_voucher_kugou":
                    ST["kugou"] += 1
                WON.append({"uid8": uid8, "nick": a["nick"], "code": pc, "label": label})
            else:
                ST["credit"] += ca
        time.sleep(1.2)
    with LOCK:
        DETAIL.append(rec)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--yes", action="store_true")
    ap.add_argument("--max", type=int, default=0, help="每号最多抽几次（0=抽到空）")
    ap.add_argument("--workers", type=int, default=5)
    a = ap.parse_args()

    accts = load_accounts()
    print("账号数: %d | 模式: %s" % (len(accts), "REAL" if a.yes else "dry-run"))
    t0 = time.time()
    with ThreadPoolExecutor(max_workers=a.workers) as ex:
        futs = [ex.submit(work, x, a.yes, a.max) for x in accts]
        for f in as_completed(futs):
            f.result()

    print("\n=== 结果 ===")
    print("非活动期: %d | config 失败: %d | 抽奖异常: %d"
          % (ST["out_period"], ST["cfg_err"], ST["draw_err"]))
    print("待抽(只读模式): %d | 剩余次数合计: %d | 单号最多剩: %d"
          % (ST["pending"], ST["bal_total"], ST["bal_max"]))
    print("★ 本次抽奖: %d 次" % ST["drawn"])
    print("中实物券: %d 张" % ST["voucher"])
    if ST["voucher"]:
        print("   瑞幸咖啡: %d" % ST["luckin"])
        print("   肯德基冰淇淋: %d" % ST["kfc_ice"])
        print("   肯德基OK餐: %d" % ST["kfc_ok"])
        print("   酷狗会员: %d" % ST["kugou"])
    print("中积分: %d" % ST["credit"])
    if WON:
        print("\n=== 本批中奖券 ===")
        for w in WON:
            print("  %s %-18s %s" % (w["uid8"], w["nick"][:18], w["label"]))
    print("\n用时 %.1f 分钟" % ((time.time() - t0) / 60))
    json.dump({"won": WON, "detail": DETAIL}, open("/tmp/school_draw_result.json", "w"),
              ensure_ascii=False, indent=1)
    print("明细 -> /tmp/school_draw_result.json")


if __name__ == "__main__":
    main()
