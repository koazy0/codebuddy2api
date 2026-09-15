const $ = (id) => document.getElementById(id);
const state = {
  tab: "overview",
  page: 1,
  pageSize: 20,
  total: 0,
  accounts: [],
  models: [],
  summary: {},
  auto: true,
  timer: null,
  adminKey: "",
  password: "",
  latestAccountId: null
};

// 兜底错误提示：任何未捕获的异常都显示给用户，而不是静默变成「点了没反应」。
window.addEventListener("error", (e) => {
  if ($("flash")) flash("页面出错：" + (e.message || "未知错误"), false);
});
window.addEventListener("unhandledrejection", (e) => {
  const reason = e && e.reason;
  if (reason && reason.unauthorized) return;
  if ($("flash")) flash("请求出错：" + (reason && reason.message ? reason.message : String(reason)), false);
});

function esc(v) {
  return String(v == null ? "" : v).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}
function flash(text, ok) {
  const el = $("flash");
  el.textContent = text || "";
  el.className = "msg " + (ok === false ? "err" : ok ? "ok" : "");
}
function fmtTime(v) {
  if (!v) return "—";
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString("zh-CN", { hour12: false });
}
function fmtNum(n, digits) {
  n = Number(n || 0);
  if (Math.abs(n) >= 1e6) return (n / 1e6).toFixed(2) + "M";
  if (Math.abs(n) >= 1e3) return (n / 1e3).toFixed(1) + "k";
  return Number(n.toFixed(digits == null ? 2 : digits)).toString();
}
function fmtPct(n) { return (Number(n || 0) * 100).toFixed(1) + "%"; }
function statusClass(s) {
  if (s === "enabled") return "ok";
  if (s === "cooldown") return "warn";
  return "bad";
}
function accountName(id) {
  const acc = state.accounts.find(a => Number(a.id) === Number(id));
  return acc ? (acc.name || acc.username || ("#" + id)) : ("#" + id);
}
function remainText(acc) {
  const monthly = Number(acc.monthly_credit_remain || 0);
  const onetime = Number(acc.onetime_credit_remain || 0);
  if (!acc.credit_synced_at && !acc.monthly_credit_total && !acc.onetime_credit_total && !acc.credit_remain) return "未同步";
  return Number(acc.credit_remain || (monthly + onetime)).toFixed(2)
    + " (月 " + monthly.toFixed(2) + " / 次 " + onetime.toFixed(2) + ")";
}

// 请求超时上限。没有它的话，任何一个挂住的请求都会让整个面板永远转圈。
const API_TIMEOUT_MS = 15000;

async function api(path, opts) {
  opts = opts || {};
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), opts.timeoutMs || API_TIMEOUT_MS);
  let res;
  try {
    res = await fetch(path, Object.assign({}, opts, {
      signal: controller.signal,
      headers: Object.assign({
        "Authorization": "Bearer " + (state.adminKey || state.password || ""),
        "X-Admin-Key": state.adminKey || "",
        "X-Dashboard-Password": state.password || "",
        "Content-Type": "application/json"
      }, opts.headers || {})
    }));
  } catch (err) {
    if (err && err.name === "AbortError") throw new Error("请求超时（" + Math.round((opts.timeoutMs || API_TIMEOUT_MS) / 1000) + "s），请稍后重试");
    throw new Error("网络错误：" + (err && err.message ? err.message : String(err)));
  } finally {
    clearTimeout(timer);
  }
  const body = await res.json().catch(() => ({}));
  if (res.status === 401 || body.code === 401) {
    const err = new Error(body.msg || "未授权");
    err.unauthorized = true;
    throw err;
  }
  if (!res.ok || (body.code && body.code !== 0)) throw new Error(body.msg || ("HTTP " + res.status));
  return body.data;
}

function modal(title, html) {
  $("modalTitle").textContent = title;
  $("modalBody").innerHTML = html;
  $("modal").classList.remove("hidden");
  document.body.classList.add("modal-open");
}
function closeModal() {
  $("modal").classList.add("hidden");
  $("modalBody").innerHTML = "";
  document.body.classList.remove("modal-open");
  delete $("modal").dataset.backdrop;
}

function renderCards(summary, settings, accounts) {
  const latestUsed = accounts.slice().sort((a, b) => new Date(b.last_used_at || 0) - new Date(a.last_used_at || 0))[0];
  const items = [
    ["账号", (summary.accounts_enabled || 0) + " / " + (summary.accounts_total || 0), "启用 / 全部"],
    ["请求", fmtNum(summary.requests, 0), "累计调用"],
    ["输入 Token", fmtNum(summary.prompt_tokens, 0), "prompt"],
    ["输出 Token", fmtNum(summary.completion_tokens, 0), "completion"],
    ["缓存命中率", fmtPct(summary.avg_cache_hit_rate), "命中 " + fmtNum(summary.cache_hit_tokens, 0) + " / 未命中 " + fmtNum(summary.cache_miss_tokens, 0)],
    ["思考 Token", fmtNum(summary.thinking_tokens, 0), "reasoning"],
    ["消耗积分", fmtNum(summary.credit, 2), "均延迟 " + Math.round(summary.avg_latency_ms || 0) + "ms"],
    ["首字 / TPS", Math.round(summary.avg_first_token_ms || 0) + "ms / " + Number(summary.avg_tokens_per_second || 0).toFixed(1), "平均"],
    ["最近票据", latestUsed ? accountName(latestUsed.id) : "—", latestUsed ? fmtTime(latestUsed.last_used_at) : "还没有请求"],
    ["上次刷新", fmtTime(settings.last_refresh_at), settings.enabled ? "定时已开" : "定时关闭"],
    ["下次刷新", fmtTime(settings.next_run_at), settings.cron || ""]
  ];
  $("cards").innerHTML = items.map(([k, v, s]) =>
    `<div class="card"><div class="k">${esc(k)}</div><div class="v">${esc(v)}</div><div class="s">${esc(s || "")}</div></div>`).join("");
}

function renderDaily(rows) {
  const max = Math.max(1, ...rows.map(r => Number(r.prompt_tokens || 0) + Number(r.completion_tokens || 0)));
  $("dailyHint").textContent = "最近 " + (rows.length || 0) + " 天";
  if (!rows.length) { $("dailyChart").innerHTML = `<div class="tiny" style="padding:14px">还没有请求</div>`; return; }
  $("dailyChart").innerHTML = rows.map(r => {
    const inH = Math.max(2, Math.round(Number(r.prompt_tokens || 0) / max * 96));
    const outH = Math.max(2, Math.round(Number(r.completion_tokens || 0) / max * 96));
    return `<div class="col" title="${esc(r.day)} · 请求 ${r.requests} · 输入 ${r.prompt_tokens} · 输出 ${r.completion_tokens} · 缓存命中 ${r.cache_hit_tokens}">
      <i style="height:${inH}px"></i><i class="out" style="height:${outH}px"></i><span class="lbl">${esc(String(r.day || "").slice(5))}</span></div>`;
  }).join("");
}

function renderCache(summary) {
  const hit = Number(summary.cache_hit_tokens || 0);
  const miss = Number(summary.cache_miss_tokens || 0);
  const total = hit + miss;
  $("cachePanel").innerHTML = `
    <div class="kv" style="padding:0;">
      <div class="k">缓存命中 token</div><div>${fmtNum(hit, 0)}</div>
      <div class="k">缓存未命中 token</div><div>${fmtNum(miss, 0)}</div>
      <div class="k">命中占比</div><div>${total ? (hit / total * 100).toFixed(1) + "%" : "—"}</div>
      <div class="k">平均请求命中率</div><div>${fmtPct(summary.avg_cache_hit_rate)}</div>
      <div class="k">缓存读取 / 写入</div><div>${fmtNum(summary.cache_read_input_tokens, 0)} / ${fmtNum(summary.cache_write_tokens, 0)}</div>
      <div class="k">cached_tokens</div><div>${fmtNum(summary.cached_tokens, 0)}</div>
    </div>
    <div class="bar" style="margin-top:12px;"><i style="width:${Math.min(100, total ? hit / total * 100 : 0)}%"></i></div>`;
}

function renderBars(el, rows, label) {
  if (!rows.length) { el.innerHTML = `<div class="tiny">暂无数据</div>`; return; }
  const max = Math.max(1, ...rows.map(r => Number(r.requests || 0)));
  el.innerHTML = rows.map(r => `
    <div class="bar-row">
      <span class="mono">${esc(label(r))}</span>
      <span class="bar"><i style="width:${Math.round(Number(r.requests || 0) / max * 100)}%"></i></span>
      <span>${fmtNum(r.requests, 0)} 次<div class="tiny">${fmtNum(r.prompt_tokens, 0)} in / ${fmtNum(r.completion_tokens, 0)} out</div></span>
    </div>`).join("");
}

function renderRefresh(settings) {
  $("lastRefresh").textContent = fmtTime(settings.last_refresh_at);
  $("nextRefresh").textContent = settings.enabled ? fmtTime(settings.next_run_at) : "已停用";
  $("cronInput").value = settings.cron || "0 3 * * *";
  $("refreshEnabled").checked = !!settings.enabled;
  $("thresholdDays").value = settings.threshold_days || 30;
  $("timeoutSeconds").value = settings.timeout_seconds || 15;
  const presets = [...$("cronPreset").options].map(o => o.value);
  $("cronPreset").value = presets.includes(settings.cron) ? settings.cron : "custom";
}

function accountCells(acc) {
  return `<td><b>${esc(acc.name || acc.username || "-")}</b><div class="tiny">#${acc.id} · weight ${acc.weight}</div></td>
    <td><span class="badge ${statusClass(acc.status)}">${esc(acc.status)}</span>${acc.last_error ? `<div class="tiny bad">${esc(acc.last_error)}</div>` : ""}</td>
    <td>${esc(remainText(acc))}<div class="tiny">${acc.credit_synced_at ? "同步 " + fmtTime(acc.credit_synced_at) : "额度未同步"}</div></td>
    <td>${fmtNum(acc.credit_used, 2)}</td>
    <td>刷新 ${fmtTime(acc.last_refresh)}<div class="tiny">JWT ${fmtTime(acc.jwt_expires_at)} · RT ${fmtTime(acc.refresh_expires_at)}</div></td>
    <td>${fmtTime(acc.last_used_at)}${acc.cooldown_until ? `<div class="tiny warn">冷却至 ${fmtTime(acc.cooldown_until)}</div>` : ""}</td>`;
}

function renderAccounts(accounts) {
  state.accounts = accounts || [];
  const latest = state.accounts.slice().sort((a, b) => new Date(b.last_used_at || 0) - new Date(a.last_used_at || 0))[0];
  state.latestAccountId = latest ? latest.id : null;
  $("accountHint").textContent = state.latestAccountId ? ("最近请求走 #" + state.latestAccountId) : "";
  $("accountBody").innerHTML = state.accounts.map(acc =>
    `<tr class="${acc.id === state.latestAccountId ? "hot" : ""}">${accountCells(acc)}</tr>`
  ).join("") || `<tr><td colspan="6" class="muted">还没有账号</td></tr>`;

  $("accountFullBody").innerHTML = state.accounts.map(acc => `
    <tr class="${acc.id === state.latestAccountId ? "hot" : ""}">
      ${accountCells(acc)}
      <td class="tiny">${esc(acc.remark || "—")}</td>
      <td class="actions">
        <button class="ghost" data-act="edit" data-id="${acc.id}">编辑</button>
        <button class="ghost" data-act="refresh" data-id="${acc.id}">刷新票据</button>
        <button class="ghost" data-act="sync" data-id="${acc.id}">同步额度</button>
        <button class="ghost" data-act="tasks" data-id="${acc.id}">积分任务</button>
        <button class="${acc.status === "enabled" ? "danger" : "ghost"}" data-act="${acc.status === "enabled" ? "disable" : "enable"}" data-id="${acc.id}">${acc.status === "enabled" ? "停用" : "启用"}</button>
        <button class="danger" data-act="delete" data-id="${acc.id}">删除</button>
      </td>
    </tr>`).join("") || `<tr><td colspan="8" class="muted">还没有账号</td></tr>`;

  const sel = $("usageAccount");
  const cur = sel.value;
  sel.innerHTML = `<option value="">全部账号</option>` + state.accounts.map(a =>
    `<option value="${a.id}">#${a.id} ${esc(a.name || a.username)}</option>`).join("");
  sel.value = cur;
}

function renderUsageModels(models) {
  const sel = $("usageModel");
  const cur = sel.value;
  sel.innerHTML = `<option value="">全部模型</option>` + (models || []).map(m =>
    `<option value="${esc(m)}">${esc(m)}</option>`).join("");
  sel.value = cur;
}

function renderUsage(page) {
  const list = page.list || [];
  state.total = page.total || 0;
  state.page = page.page || 1;
  state.pageSize = page.pageSize || state.pageSize;
  $("usageBody").innerHTML = list.map(item => `<tr>
    <td class="tiny">${fmtTime(item.created_at)}</td>
    <td>${esc(item.account_name || accountName(item.account_id))}<div class="tiny">#${item.account_id}</div></td>
    <td class="tiny">${esc(item.protocol || "")}${item.stream ? " · stream" : ""}</td>
    <td>${esc(item.model || "-")}${item.upstream_model && item.upstream_model !== item.model ? `<div class="tiny">→ ${esc(item.upstream_model)}</div>` : ""}</td>
    <td>${fmtNum(item.prompt_tokens, 0)}<div class="tiny">req ${fmtNum(item.request_tokens, 0)}B</div></td>
    <td>${fmtNum(item.cache_hit_tokens, 0)}<div class="tiny">${fmtPct(item.cache_hit_rate)}</div></td>
    <td>${fmtNum(item.completion_tokens, 0)}<div class="tiny">think ${fmtNum(item.thinking_tokens, 0)}</div></td>
    <td>${fmtNum(item.credit, 2)}<div class="tiny">${esc(item.credit_source || "")}</div></td>
    <td>${item.latency_ms || 0}ms<div class="tiny">首字 ${item.first_token_ms || 0}ms · ${Number(item.tokens_per_second || 0).toFixed(1)} tps</div></td>
    <td class="${item.status_code === 200 ? "ok" : "bad"}">${item.status_code}${item.error ? `<div class="tiny bad">${esc(String(item.error).slice(0, 60))}</div>` : ""}</td>
    <td class="actions">
      <button class="ghost" data-detail="${item.id}">详情</button>
      <button class="danger" data-del="${item.id}">删</button>
    </td>
  </tr>`).join("") || `<tr><td colspan="11" class="muted">暂无记录</td></tr>`;
  const pages = Math.max(1, Math.ceil(state.total / state.pageSize));
  $("usageMeta").textContent = `第 ${state.page} / ${pages} 页，共 ${state.total} 条`;
}

async function showDetail(id) {
  const item = await api("/admin/usage/" + id);
  let usageText = "(上游未返回 usage)";
  if (item.raw_usage) {
    try { usageText = JSON.stringify(JSON.parse(item.raw_usage), null, 2); } catch (err) { usageText = item.raw_usage; }
  }
  modal("请求 #" + id, `
    <div class="kv">
      <div class="k">时间</div><div>${fmtTime(item.created_at)}</div>
      <div class="k">账号</div><div>${esc(item.account_name || accountName(item.account_id))} (#${item.account_id})</div>
      <div class="k">协议 / 模型</div><div>${esc(item.protocol)} · ${esc(item.model)} → ${esc(item.upstream_model)}${item.stream ? " · stream" : ""}</div>
      <div class="k">Token</div><div>prompt ${item.prompt_tokens} / completion ${item.completion_tokens} / total ${item.total_tokens} / thinking ${item.thinking_tokens}</div>
      <div class="k">缓存</div><div>命中 ${item.cache_hit_tokens} / 未命中 ${item.cache_miss_tokens} / 命中率 ${fmtPct(item.cache_hit_rate)} / 读 ${item.cache_read_input_tokens} / 写 ${item.cache_write_tokens} / cached ${item.cached_tokens}</div>
      <div class="k">性能</div><div>首字 ${item.first_token_ms}ms · 总 ${item.latency_ms}ms · ${Number(item.tokens_per_second || 0).toFixed(1)} tps</div>
      <div class="k">积分</div><div>${item.credit} (${esc(item.credit_source || "untracked")}) · 月 ${item.credit_monthly} / 次 ${item.credit_onetime} · 预估 ${Number(item.estimated_credit || 0).toFixed(4)}</div>
      <div class="k">请求 ID</div><div class="mono">${esc(item.request_id || "—")}</div>
      <div class="k">来源</div><div>${esc(item.client_ip || "—")} · ${esc(item.user_agent || "—")}</div>
      ${item.error ? `<div class="k">错误</div><div class="bad">${esc(item.error)}</div>` : ""}
    </div>
    <div class="body">
      <div class="tiny" style="margin-bottom:6px;">请求内容（响应体大小 ${item.response_tokens || 0} B）</div>
      <pre>${esc(item.request_preview || "(未记录)")}</pre>
      <div class="tiny" style="margin:12px 0 6px;">响应内容</div>
      <pre>${esc(item.response_preview || "(未记录)")}</pre>
      <div class="tiny" style="margin:12px 0 6px;">上游 usage 原文</div>
      <pre>${esc(usageText)}</pre>
    </div>`);
}

function renderModels(models) {
  state.models = models || [];
  $("modelBody").innerHTML = state.models.map(m => `<tr>
    <td class="mono">${esc(m.model_id)}</td>
    <td>${esc(m.display_name || "")}</td>
    <td>${fmtNum(m.max_input, 0)}</td>
    <td>${fmtNum(m.max_output, 0)}</td>
    <td class="tiny">${m.vision ? "vision " : ""}${m.reasoning ? "reasoning" : ""}</td>
    <td class="${m.enabled ? "ok" : "bad"}">${m.enabled ? "启用" : "停用"}</td>
    <td>${m.sort}</td>
    <td class="actions">
      <button class="ghost" data-model-edit="${esc(m.model_id)}">编辑</button>
      <button class="${m.enabled ? "danger" : "ghost"}" data-model-toggle="${esc(m.model_id)}" data-enabled="${m.enabled ? 1 : 0}">${m.enabled ? "停用" : "启用"}</button>
    </td>
  </tr>`).join("") || `<tr><td colspan="8" class="muted">暂无模型</td></tr>`;
}

function renderAccess(access) {
  $("dashTitle").value = access.title || "";
  $("dashRequireAdmin").checked = !!access.require_admin_key;
  const title = access.title || "CodeBuddy2API 控制台";
  $("pageTitle").textContent = title;
  $("gateTitle").textContent = title;
  document.title = title;
  $("accessHint").innerHTML = access.password_set
    ? `已设置面板密码<span class="strength ${esc(access.password_strength || "weak")}">${esc(access.password_strength || "")}</span>`
    : "尚未设置面板密码，当前仅靠 Admin Key";
}

function renderRuntime(summary, settings) {
  const rows = [
    ["请求总数", fmtNum(summary.requests, 0)],
    ["输入 / 输出 token", fmtNum(summary.prompt_tokens, 0) + " / " + fmtNum(summary.completion_tokens, 0)],
    ["缓存命中 / 未命中", fmtNum(summary.cache_hit_tokens, 0) + " / " + fmtNum(summary.cache_miss_tokens, 0)],
    ["平均延迟", Math.round(summary.avg_latency_ms || 0) + " ms"],
    ["平均首字", Math.round(summary.avg_first_token_ms || 0) + " ms"],
    ["平均 TPS", Number(summary.avg_tokens_per_second || 0).toFixed(1)],
    ["累计积分", fmtNum(summary.credit, 2)],
    ["票据刷新计划", (settings.enabled ? settings.cron : "已停用") + " · 上次 " + fmtTime(settings.last_refresh_at)],
    ["下次刷新", fmtTime(settings.next_run_at)]
  ];
  $("runtimeInfo").innerHTML = rows.map(([k, v]) => `<div class="k">${esc(k)}</div><div>${esc(v)}</div>`).join("");
}

function usageQuery() {
  const params = new URLSearchParams();
  params.set("page", state.page);
  params.set("page_size", state.pageSize);
  const acc = $("usageAccount").value;
  const model = $("usageModel").value;
  const proto = $("usageProtocol").value;
  const status = $("usageStatus").value;
  const range = $("usageRange").value;
  const keyword = $("usageKeyword").value.trim();
  if (acc) params.set("account_id", acc);
  if (model) params.set("model", model);
  if (proto) params.set("protocol", proto);
  if (status) params.set("status", status);
  if (keyword) params.set("keyword", keyword);
  if (range) params.set("start", new Date(Date.now() - Number(range) * 86400000).toISOString());
  return params;
}

async function loadAll() {
  // 逐个接口容错：某个接口失败只影响它自己的模块，
  // 不再像 Promise.all 那样一个失败就整页白屏。
  const reqs = {
    summary: api("/admin/usage/summary"),
    settings: api("/admin/settings/refresh"),
    accounts: api("/admin/accounts"),
    usage: api("/admin/usage?" + usageQuery().toString()),
    models: api("/admin/models"),
    daily: api("/admin/stats/daily?days=14"),
    byModel: api("/admin/stats/models?limit=8"),
    byAccount: api("/admin/stats/accounts?limit=10"),
    usageModels: api("/admin/usage/models"),
    access: api("/admin/settings/access")
  };
  const keys = Object.keys(reqs);
  const settled = await Promise.allSettled(keys.map(k => reqs[k]));
  const out = {};
  const failed = [];
  keys.forEach((k, i) => {
    const r = settled[i];
    if (r.status === "fulfilled") { out[k] = r.value; return; }
    out[k] = null;
    failed.push(k + ": " + (r.reason && r.reason.message ? r.reason.message : String(r.reason)));
  });
  // 授权失效是全局问题，直接抛出去让调用方走重新登录。
  const denied = settled.find(r => r.status === "rejected" && r.reason && r.reason.unauthorized);
  if (denied) throw denied.reason;

  const { summary, settings, accounts, usage, models, daily, byModel, byAccount, usageModels, access } = out;
  if (failed.length) flash("部分数据加载失败（" + failed.length + "/" + keys.length + "）：" + failed[0], false);
  state.summary = summary || {};
  renderRefresh(settings || {});
  renderAccounts(accounts || []);
  renderCards(summary || {}, settings || {}, accounts || []);
  renderDaily(daily || []);
  renderCache(summary || {});
  renderBars($("modelBars"), byModel || [], r => r.model || "-");
  renderBars($("accountBars"), byAccount || [], r => r.account_name || ("#" + r.account_id));
  renderUsage(usage || {});
  renderUsageModels(usageModels || []);
  renderModels(models || []);
  renderAccess(access || {});
  renderRuntime(summary || {}, settings || {});
}

async function withFlash(fn, okMsg, opts) {
  try {
    flash("处理中...");
    const result = await fn();
    if (okMsg) flash(okMsg, true);
    // 有些调用本身就是重新加载（例如「刷新数据」），不必再多刷一遍。
    if (!(opts && opts.skipReload)) await loadAll();
    return result;
  } catch (err) {
    flash(err.message || String(err), false);
    throw err;
  }
}

async function signOut(message) {
  localStorage.removeItem("cb2api_admin_key");
  localStorage.removeItem("cb2api_password");
  state.adminKey = "";
  state.password = "";
  if (state.timer) {
    clearInterval(state.timer);
    state.timer = null;
  }
  $("gate").classList.remove("hidden");
  if (message) $("gateErr").textContent = message;
}

// verifyCredentials 只做「凭证对不对」的校验，不拉数据，
// 这样打开页面时可以先静默确认登录态，失败就直接停在登录框等用户输入。
async function verifyCredentials(password, adminKey) {
  const res = await fetch("/admin/auth/verify", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ password: password, admin_key: adminKey })
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok || (body.code && body.code !== 0)) throw new Error(body.msg || "登录失败");
  return body.data;
}

// doLogin 负责「校验 -> 存凭证 -> 拉数据 -> 关门」的完整流程。
// 按钮会在这期间禁用，避免连点触发多次并发加载。
async function doLogin(password, adminKey) {
  const btn = $("gateBtn");
  if (btn.disabled) return;
  $("gateErr").textContent = "";
  btn.disabled = true;
  const label = btn.textContent;
  btn.textContent = "登录中…";
  try {
    await verifyCredentials(password, adminKey);
    state.password = password;
    state.adminKey = adminKey;
    localStorage.setItem("cb2api_password", password);
    localStorage.setItem("cb2api_admin_key", adminKey);
    $("gate").classList.add("hidden");
    await loadAll();
    flash("已加载 " + new Date().toLocaleTimeString("zh-CN", { hour12: false }), true);
  } catch (err) {
    $("gate").classList.remove("hidden");
    $("gateErr").textContent = err.message;
  } finally {
    btn.disabled = false;
    btn.textContent = label;
  }
}

$("gateBtn").onclick = () => doLogin($("gatePassword").value, $("gateKey").value.trim());
$("gateKey").addEventListener("keydown", (e) => { if (e.key === "Enter") $("gateBtn").click(); });
$("gatePassword").addEventListener("keydown", (e) => { if (e.key === "Enter") $("gateBtn").click(); });
$("logoutBtn").onclick = () => signOut("已退出，请重新登录。");
$("reloadBtn").onclick = () => withFlash(loadAll, "已刷新", { skipReload: true });
$("autoBtn").onclick = () => {
  state.auto = !state.auto;
  $("autoBtn").textContent = "自动刷新：" + (state.auto ? "开" : "关");
};
$("modalClose").onclick = closeModal;
// 弹窗顶部可能被滚动内容顶走，这里再兜一层：优先用事件委托处理关闭，
// 并支持键盘 ESC 与点击遮罩空白处关闭。
$("modal").addEventListener("click", (e) => {
  const closer = e.target.closest && e.target.closest("[data-close-modal]");
  if (closer) closeModal();
});
// 只有点到遮罩本身才关闭，避免在弹窗内容里拖动/选中文字时误关。
$("modal").addEventListener("mousedown", (e) => { if (e.target === $("modal")) $("modal").dataset.backdrop = "1"; });
$("modal").addEventListener("mouseup", (e) => {
  if (e.target === $("modal") && $("modal").dataset.backdrop === "1") closeModal();
  delete $("modal").dataset.backdrop;
});
// ESC 关闭弹窗；弹窗没开时不影响其它按键。
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && !$("modal").classList.contains("hidden")) closeModal();
});

$("tabs").onclick = (e) => {
  const btn = e.target.closest("button[data-tab]");
  if (!btn) return;
  state.tab = btn.dataset.tab;
  [...$("tabs").querySelectorAll("button")].forEach(b => b.classList.toggle("active", b === btn));
  ["overview", "requests", "accounts", "models", "settings"].forEach(t =>
    $("tab-" + t).classList.toggle("hidden", t !== state.tab));
  // 切到「面板设置」时同步刷新登录设置，避免显示其它标签页留下的旧值。
  if (state.tab === "settings") {
    api("/admin/settings/access")
      .then((access) => renderAccess(access || {}))
      .catch((err) => { if (err && err.unauthorized) signOut("登录已失效，请重新登录。"); });
  }
};

$("cronPreset").onchange = () => {
  if ($("cronPreset").value !== "custom") $("cronInput").value = $("cronPreset").value;
};
$("saveRefresh").onclick = () => withFlash(async () => {
  await api("/admin/settings/refresh", {
    method: "PUT",
    body: JSON.stringify({
      enabled: $("refreshEnabled").checked,
      cron: $("cronInput").value.trim(),
      threshold_days: Number($("thresholdDays").value),
      timeout_seconds: Number($("timeoutSeconds").value)
    })
  });
}, "刷新时间已保存");
$("runRefresh").onclick = () => withFlash(async () => {
  const data = await api("/admin/refresh", { method: "POST" });
  flash("扫描 " + data.scanned + "，刷新 " + data.refreshed + "，失败 " + data.failed, true);
}, "");
// 同步额度：返回 {ok, failed, total}，把统计如实显示出来。
// 账号多的时候「有几个失败」才是关键信息，只弹「已同步」等于没说。
$("syncCredit").onclick = () => withFlash(async () => {
  // 全量同步要逐个账号请求上游，远超默认 15s。
  const r = await api("/admin/sync-credit", { method: "POST", timeoutMs: 300000 });
  const failed = Number(r && r.failed) || 0;
  const ok = Number(r && r.ok) || 0;
  const total = Number(r && r.total) || 0;
  const detail = failed ? `同步完成：${ok}/${total} 成功，${failed} 个失败（详见账号列表的错误信息）`
                        : `同步完成：${ok}/${total} 个账号额度已更新`;
  flash(detail, !failed);
}, "");
// 看门狗：后端回 {round:{checked,recovered,disabled,skipped}, accounts:[...]}。
// 三项统计都要看，尤其 disabled —— 那代表有账号刚被判死。
// ---------------------------------------------------------------------------
// 批量做任务
//
// 单账号要 1-2 分钟，多账号串在一个请求里必然被反向代理按空闲超时掐断，
// 所以后端是「启动即返回 + 后台跑」，这里靠轮询看进度。
// ---------------------------------------------------------------------------

const BATCH_POLL_MS = 3000;
let batchTimer = null;

function stopBatchPoll() {
  if (batchTimer) { clearInterval(batchTimer); batchTimer = null; }
}

function batchStatusText(s) {
  if (!s) return "";
  const parts = (s.results || []).map(r => {
    const icon = r.status === "done" ? "✓" : r.status === "failed" ? "✕"
      : r.status === "skipped" ? "—" : r.status === "running" ? "…" : "·";
    const tail = r.message ? " " + esc(r.message) : "";
    return `${icon} ${esc(r.account || ("#" + r.account_id))}${tail}`;
  });
  return parts.join("<br>");
}

async function batchTasksFlow() {
  const st = await api("/admin/tasks/batch", { method: "POST", body: JSON.stringify({}) });
  modal("批量做任务", `
    <div class="body">
      <p class="tiny">共 <b>${st.total}</b> 个账号，并发 <b>${st.concurrent}</b>。
        单账号约 1-2 分钟，可关闭本窗口，任务在后台继续。</p>
      <div class="row" style="margin:10px 0;">
        <span id="batchProgress" class="tiny"></span>
      </div>
      <div class="bar" style="margin-bottom:10px"><i id="batchBar" style="width:0%"></i></div>
      <div id="batchList" class="tiny" style="max-height:48vh;overflow:auto;line-height:1.9"></div>
    </div>`);

  const paint = (s) => {
    const pct = s.total ? Math.round((s.done / s.total) * 100) : 0;
    const el = $("batchProgress");
    if (el) {
      el.textContent = `${s.done}/${s.total} 完成 · +${s.credit_gained} 分 +${s.energy_gained} 能`
        + (s.running ? "" : "（已结束）");
    }
    const bar = $("batchBar");
    if (bar) bar.style.width = pct + "%";
    const list = $("batchList");
    if (list) list.innerHTML = batchStatusText(s);
  };

  const poll = async () => {
    try {
      const s = await api("/admin/tasks/batch");
      if (s && s.none) { stopBatchPoll(); return; }
      paint(s);
      if (!s.running) {
        stopBatchPoll();
        flash(`批量任务完成：+${s.credit_gained} 分 +${s.energy_gained} 能`, true);
        loadAll().catch(() => {});
      }
    } catch (err) {
      stopBatchPoll();
      flash(err.message || String(err), false);
    }
  };

  paint(st);
  stopBatchPoll();
  batchTimer = setInterval(poll, BATCH_POLL_MS);
  poll();
}
$("batchTasks").onclick = () => batchTasksFlow().catch(err => flash(err.message || String(err), false));

$("runWatchdog").onclick = () => withFlash(async () => {
  const r = await api("/admin/watchdog", { method: "POST", timeoutMs: 180000 });
  const round = (r && r.round) || {};
  if (round.skipped) {
    flash("上一轮看门狗还在跑，本轮已跳过", false);
    return;
  }
  const checked = Number(round.checked) || 0;
  const recovered = Number(round.recovered) || 0;
  const disabled = Number(round.disabled) || 0;
  flash(`看门狗完成：检查 ${checked} 个账号，续期/解冻 ${recovered} 个，判定异常 ${disabled} 个`, disabled === 0);
}, "");

$("saveAccess").onclick = () => withFlash(async () => {
  const pwd = $("dashPassword").value;
  const pwd2 = $("dashPassword2").value;
  if (pwd !== pwd2) throw new Error("两次输入的密码不一致");
  const body = {
    title: $("dashTitle").value.trim(),
    require_admin_key: $("dashRequireAdmin").checked
  };
  if (pwd) body.password = pwd;
  await api("/admin/settings/access", { method: "PUT", body: JSON.stringify(body) });
  $("dashPassword").value = "";
  $("dashPassword2").value = "";
}, "面板设置已保存");
$("clearPassword").onclick = () => {
  if (!$("dashRequireAdmin").checked) { flash("请先勾选「同时校验 Admin Key」，否则面板会失去保护", false); return; }
  if (!confirm("清空面板密码？之后只能靠 Admin Key 登录。")) return;
  withFlash(() => api("/admin/settings/access", {
    method: "PUT",
    body: JSON.stringify({ title: $("dashTitle").value.trim(), password: "", require_admin_key: true })
  }), "面板密码已清空");
};

$("usageFilter").onclick = () => { state.page = 1; withFlash(loadAll); };
$("usageReset").onclick = () => {
  ["usageAccount", "usageModel", "usageProtocol", "usageStatus", "usageRange", "usageKeyword"].forEach(id => { $(id).value = ""; });
  state.page = 1;
  withFlash(loadAll);
};
$("usagePageSize").onchange = () => { state.pageSize = Number($("usagePageSize").value); state.page = 1; withFlash(loadAll); };
$("prevPage").onclick = () => { if (state.page > 1) { state.page -= 1; withFlash(loadAll); } };
$("nextPage").onclick = () => {
  if (state.page * state.pageSize < state.total) { state.page += 1; withFlash(loadAll); }
};
$("usageDelete").onclick = async () => {
  const params = usageQuery();
  params.delete("page");
  params.delete("page_size");
  if (!params.toString()) { flash("请先设置筛选条件，避免误删全部记录", false); return; }
  if (!confirm("按当前筛选条件删除记录？此操作不可恢复。")) return;
  await withFlash(async () => {
    const data = await api("/admin/usage?" + params.toString(), { method: "DELETE" });
    flash("已删除 " + data.deleted + " 条", true);
  }, "");
};
$("usageBody").onclick = async (e) => {
  const del = e.target.closest("button[data-del]");
  if (del) {
    if (!confirm("删除这条记录？")) return;
    await withFlash(() => api("/admin/usage/" + del.dataset.del, { method: "DELETE" }), "已删除 #" + del.dataset.del);
    return;
  }
  const detail = e.target.closest("button[data-detail]");
  if (detail) {
    try { await showDetail(detail.dataset.detail); } catch (err) { flash(err.message, false); }
  }
};

function accountForm(acc) {
  const a = acc || {};
  modal(acc ? ("编辑账号 #" + a.id) : "新增账号", `
    <div class="body">
      <div class="field"><label>名称</label><input id="frmName" value="${esc(a.name || "")}"></div>
      <div class="field"><label>JWT（accessToken）</label><input id="frmJwt" value="" placeholder="${acc ? "留空表示不修改" : "必填"}"></div>
      <div class="field"><label>Refresh Token</label><input id="frmRt" value="" placeholder="留空表示不修改"></div>
      <div class="field"><label>Session Cookie</label><input id="frmCookie" value="" placeholder="可选"></div>
      <div class="row">
        <div class="field"><label>权重</label><input id="frmWeight" type="number" min="1" value="${a.weight || 1}"></div>
        <div class="field"><label>状态</label>
          <select id="frmStatus">
            <option value="enabled">enabled</option>
            <option value="disabled">disabled</option>
            <option value="cooldown">cooldown</option>
          </select>
        </div>
      </div>
      <div class="field"><label>备注</label><input id="frmRemark" value="${esc(a.remark || "")}"></div>
      <div class="row"><button id="frmSave" type="button">保存</button></div>
    </div>`);
  $("frmStatus").value = a.status || "enabled";
  $("frmSave").onclick = async () => {
    const body = {
      name: $("frmName").value.trim(),
      weight: Number($("frmWeight").value || 1),
      status: $("frmStatus").value,
      remark: $("frmRemark").value.trim()
    };
    if ($("frmJwt").value.trim()) body.jwt = $("frmJwt").value.trim();
    if ($("frmRt").value.trim()) body.refresh_token = $("frmRt").value.trim();
    if ($("frmCookie").value.trim()) body.session_cookie = $("frmCookie").value.trim();
    if (!acc && !body.jwt) { flash("新增账号必须填 JWT", false); return; }
    const saveBtn = $("frmSave");
    if (saveBtn.disabled) return;
    saveBtn.disabled = true;
    try {
      await withFlash(() => api(acc ? "/admin/accounts/" + a.id : "/admin/accounts", {
        method: acc ? "PUT" : "POST",
        body: JSON.stringify(body)
      }), acc ? "账号已保存" : "账号已新增");
      closeModal();
    } catch (err) { /* flash 已提示 */ } finally {
      saveBtn.disabled = false;
    }
  };
}

async function accountAction(btn) {
  const id = btn.dataset.id;
  const act = btn.dataset.act;
  const acc = state.accounts.find(a => Number(a.id) === Number(id));
  if (act === "edit") { accountForm(acc); return; }
  if (act === "tasks") { await taskCenter(id, acc); return; }
  if (act === "delete") {
    if (!confirm("删除账号 #" + id + "？")) return;
    await withFlash(() => api("/admin/accounts/" + id, { method: "DELETE" }), "账号 #" + id + " 已删除");
    return;
  }
  const path = act === "refresh" ? `/admin/accounts/${id}/refresh`
    : act === "sync" ? `/admin/accounts/${id}/sync-credit`
    : `/admin/accounts/${id}/${act}`;
  await withFlash(() => api(path, { method: "POST" }), "账号 #" + id + " 已更新");
}

// ---------------------------------------------------------------------------
// 积分任务中心
//
// 任务进度由上游按「行为事件」异步聚合，所以跑完要等一下才看到结果。
// 「一键完成」= 报名 → 上报行为 → 等计分 → 自动领奖，全程幂等，可重复点。
// ---------------------------------------------------------------------------

function taskRows(tasks) {
  if (!tasks || !tasks.length) return `<tr><td colspan="5" class="muted">没有任务</td></tr>`;
  return tasks.map(t => {
    const pct = t.target > 0 ? Math.min(100, Math.round((t.current / t.target) * 100)) : 0;
    const bar = t.target > 0
      ? `<div class="tbar"><i style="width:${pct}%"></i></div><span class="tiny">${t.current}/${t.target}</span>`
      : `<span class="tiny muted">—</span>`;
    const badge = t.claimed ? `<span class="badge ok">已领取</span>`
      : t.claimable ? `<span class="badge warn">可领取</span>`
      : `<span class="badge">未完成</span>`;
    return `<tr>
      <td><b>${esc(t.title || t.task_code)}</b><div class="tiny mono">${esc(t.task_code)}</div></td>
      <td>${bar}</td>
      <td>${badge}</td>
      <td class="tiny">${t.credit ? "+" + t.credit + " 分" : ""}${t.energy ? " +" + t.energy + " 能" : ""}</td>
      <td>${t.claimable ? `<button class="ghost" data-claim="${esc(t.task_code)}">领取</button>` : ""}</td>
    </tr>`;
  }).join("");
}

async function taskCenter(id, acc) {
  modal("积分任务 · " + (acc ? (acc.name || acc.username) : ("#" + id)), `<div class="body"><p class="tiny">加载中…</p></div>`);
  const render = (data) => {
    const tasks = (data && data.tasks) || [];
    const pending = (data && data.pending_credit) || 0;
    const done = tasks.filter(t => t.claimed).length;
    $("modalBody").innerHTML = `
      <div class="body">
        <p class="tiny">账号 <b>#${esc(String(id))}</b> · 已完成 ${done}/${tasks.length} · 待领合计 <b>${pending}</b> 分</p>
        <div class="row" style="margin:10px 0;">
          <button id="taskRun" type="button">一键完成全部</button>
          <button id="taskAccept" class="ghost" type="button">全部报名</button>
          <button id="taskRefresh" class="ghost" type="button">刷新</button>
        </div>
        <div id="taskMsg" class="tiny"></div>
        <div style="max-height:52vh;overflow:auto;margin-top:8px">
          <table class="data">
            <thead><tr><th>任务</th><th>进度</th><th>状态</th><th>奖励</th><th></th></tr></thead>
            <tbody id="taskBody">${taskRows(tasks)}</tbody>
          </table>
        </div>
        <p class="tiny muted" style="margin-top:8px">任务进度由上游异步计分，执行后需等待数秒才会刷新。重复执行不会重复加分。</p>
      </div>`;

    const msg = (text, ok) => { const el = $("taskMsg"); if (el) { el.textContent = text; el.className = "tiny " + (ok === false ? "bad" : "ok"); } };

    $("taskRefresh").onclick = async () => {
      try { render(await api(`/admin/accounts/${id}/tasks`)); } catch (e) { msg(e.message, false); }
    };
    $("taskAccept").onclick = async (e) => {
      e.target.disabled = true;
      try {
        const r = await api(`/admin/accounts/${id}/tasks/accept`, { method: "POST", timeoutMs: 120000 });
        msg(`已报名 ${r.accepted} 个任务`, true);
        render(await api(`/admin/accounts/${id}/tasks`));
      } catch (err) { msg(err.message, false); e.target.disabled = false; }
    };
    $("taskRun").onclick = async (e) => {
      e.target.disabled = true;
      msg("正在执行… 各任务之间有节流间隔，可能需要 1-2 分钟，请勿关闭页面。");
      try {
        // 任务执行含多处节流等待，远超默认 15s，必须放宽超时。
        const r = await api(`/admin/accounts/${id}/tasks/run`, {
          method: "POST",
          body: JSON.stringify({}),
          timeoutMs: 900000
        });
        // 后端的前置校验（缺 userId、账号正忙）走 error 字段而不是 HTTP 错误码，
        // 直接展示它，否则用户只看到「领取 0 个」而不知道原因。
        if (r.error) {
          msg(r.error, false);
          render(await api(`/admin/accounts/${id}/tasks`));
          e.target.disabled = false;
          return;
        }
        const failed = (r.results || []).filter(x => x.error).length;
        msg(`完成：领取 ${(r.claimed || []).length} 个任务，+${r.credit_gained} 分 +${r.energy_gained} 能${failed ? `，${failed} 项失败` : ""}`, !failed);
        render(await api(`/admin/accounts/${id}/tasks`));
      } catch (err) { msg(err.message, false); e.target.disabled = false; }
    };
    $("taskBody").onclick = async (e) => {
      const b = e.target.closest("button[data-claim]");
      if (!b) return;
      b.disabled = true;
      try {
        const r = await api(`/admin/accounts/${id}/tasks/${encodeURIComponent(b.dataset.claim)}/claim`, { method: "POST", timeoutMs: 60000 });
        msg(r.already_claimed ? "该任务已领取过" : `已领取 +${r.credit} 分 +${r.energy} 能`, true);
        render(await api(`/admin/accounts/${id}/tasks`));
      } catch (err) { msg(err.message, false); b.disabled = false; }
    };
  };
  render(await api(`/admin/accounts/${id}/tasks`));
}
$("accountBody").onclick = (e) => { const b = e.target.closest("button"); if (b) accountAction(b).catch(() => {}); };
$("accountFullBody").onclick = (e) => { const b = e.target.closest("button"); if (b) accountAction(b).catch(() => {}); };
$("accountNew").onclick = () => accountForm(null);
$("accountImport").onclick = () => {
  modal("批量导入账号", `
    <div class="body">
      <p class="tiny">随便粘贴 CodeBuddy / 控制台 JSON。只抽 <code>accessToken</code> / <code>jwt</code> / <code>refreshToken</code> / <code>nickname</code> 这些关键字段。先识别，确认后再导入。</p>
      <textarea id="frmImport" rows="12" style="width:100%" class="mono" placeholder='{"auth":{"accessToken":"...","refreshToken":"..."},"account":{"nickname":"Ana"}}'></textarea>
      <div id="frmImportPreview" class="tiny" style="margin-top:10px">还没有识别。</div>
      <div class="row" style="margin-top:10px;">
        <button id="frmImportScan" type="button">识别账号</button>
        <button id="frmImportSave" class="ghost" type="button" disabled>确认导入</button>
      </div>
    </div>`);
  let payload = null;
  const previewBox = $("frmImportPreview");
  const saveBtn = $("frmImportSave");
  const resetPreview = () => {
    payload = null;
    saveBtn.disabled = true;
    previewBox.textContent = "内容已改，请重新识别。";
  };
  $("frmImport").addEventListener("input", resetPreview);
  $("frmImportScan").onclick = async () => {
    let parsed;
    try { parsed = JSON.parse($("frmImport").value); } catch (err) { flash("JSON 解析失败：" + err.message, false); return; }
    const scanBtn = $("frmImportScan");
    scanBtn.disabled = true;
    saveBtn.disabled = true;
    previewBox.textContent = "识别中...";
    try {
      const data = await api("/admin/accounts/import/preview", { method: "POST", body: JSON.stringify(parsed) });
      const list = data.accounts || [];
      if (!list.length) {
        payload = null;
        previewBox.textContent = "没有识别到 accessToken / jwt。";
        flash("没有可导入的账号", false);
        return;
      }
      payload = parsed;
      saveBtn.disabled = false;
      previewBox.innerHTML = "识别到 <b>" + list.length + "</b> 个账号：<br>" + list.map(item => {
        const refresh = item.has_refresh ? ("refresh " + esc(item.refresh_token || "有")) : "无 refresh";
        return "· " + esc(item.name || item.username || "imported") + "　jwt " + esc(item.jwt || "") + "　" + refresh;
      }).join("<br>");
      flash("识别到 " + list.length + " 个账号，确认后再导入", true);
    } catch (err) {
      payload = null;
      previewBox.textContent = err.message || String(err);
      flash(err.message || String(err), false);
    } finally {
      scanBtn.disabled = false;
    }
  };
  saveBtn.onclick = async () => {
    if (!payload) { flash("请先识别账号", false); return; }
    if (saveBtn.disabled) return;
    saveBtn.disabled = true;
    try {
      const res = await withFlash(async () => {
        return api("/admin/accounts/import", { method: "POST", body: JSON.stringify(payload) });
      }, "");
      flash("已导入 " + (res.created || 0) + " 个，更新 " + (res.updated || 0) + " 个", true);
      closeModal();
    } catch (err) { /* flash 已提示 */ } finally {
      saveBtn.disabled = false;
    }
  };
};

function modelForm(model) {
  const m = model || {};
  modal(model ? ("编辑模型 " + m.model_id) : "新增模型", `
    <div class="body">
      <div class="field"><label>Model ID</label><input id="mId" value="${esc(m.model_id || "")}" ${model ? "readonly" : ""}></div>
      <div class="field"><label>显示名</label><input id="mName" value="${esc(m.display_name || "")}"></div>
      <div class="row">
        <div class="field"><label>最大输入</label><input id="mIn" type="number" value="${m.max_input || 0}"></div>
        <div class="field"><label>最大输出</label><input id="mOut" type="number" value="${m.max_output || 0}"></div>
        <div class="field"><label>排序</label><input id="mSort" type="number" value="${m.sort || 0}"></div>
      </div>
      <div class="row" style="margin-bottom:10px;">
        <label class="tiny"><input id="mVision" type="checkbox" style="min-width:0" ${m.vision ? "checked" : ""}> vision</label>
        <label class="tiny"><input id="mReasoning" type="checkbox" style="min-width:0" ${m.reasoning ? "checked" : ""}> reasoning</label>
        <label class="tiny"><input id="mEnabled" type="checkbox" style="min-width:0" ${m.enabled !== false ? "checked" : ""}> enabled</label>
      </div>
      <div class="field"><label>备注</label><input id="mRemark" value="${esc(m.remark || "")}"></div>
      <div class="row"><button id="mSave" type="button">保存</button></div>
    </div>`);
  $("mSave").onclick = async () => {
    const body = {
      model_id: $("mId").value.trim(),
      display_name: $("mName").value.trim(),
      max_input: Number($("mIn").value || 0),
      max_output: Number($("mOut").value || 0),
      vision: $("mVision").checked,
      reasoning: $("mReasoning").checked,
      enabled: $("mEnabled").checked,
      sort: Number($("mSort").value || 0),
      remark: $("mRemark").value.trim()
    };
    if (!body.model_id) { flash("Model ID 必填", false); return; }
    const mSaveBtn = $("mSave");
    if (mSaveBtn.disabled) return;
    mSaveBtn.disabled = true;
    try {
      await withFlash(() => api("/admin/models", { method: "PUT", body: JSON.stringify(body) }), "模型已保存");
      closeModal();
    } catch (err) { /* flash 已提示 */ } finally {
      mSaveBtn.disabled = false;
    }
  };
}
$("modelNew").onclick = () => modelForm(null);
$("modelsReload").onclick = () => withFlash(loadAll, "已重新加载");
$("modelBody").onclick = async (e) => {
  const edit = e.target.closest("button[data-model-edit]");
  if (edit) { modelForm(state.models.find(m => m.model_id === edit.dataset.modelEdit)); return; }
  const toggle = e.target.closest("button[data-model-toggle]");
  if (toggle) {
    const m = state.models.find(x => x.model_id === toggle.dataset.modelToggle);
    if (!m) return;
    const next = Object.assign({}, m);
    next.enabled = toggle.dataset.enabled !== "1";
    await withFlash(() => api("/admin/models", { method: "PUT", body: JSON.stringify(next) }), "模型状态已更新");
  }
};

async function boot() {
  const savedPassword = localStorage.getItem("cb2api_password") || "";
  const savedKey = localStorage.getItem("cb2api_admin_key") || "";
  $("gatePassword").value = savedPassword;
  $("gateKey").value = savedKey;
  if (!savedPassword && !savedKey) return;
  // 打开页面时先静默校验凭证：有效就直接进面板，
  // 失效就停在登录框，不会「自动登录卡住」让人输不进密码。
  state.password = savedPassword;
  state.adminKey = savedKey;
  try {
    await verifyCredentials(savedPassword, savedKey);
    $("gate").classList.add("hidden");
    await loadAll();
    flash("已加载 " + new Date().toLocaleTimeString("zh-CN", { hour12: false }), true);
  } catch (err) {
    signOut(err && err.unauthorized ? "登录已失效，请重新输入。" : "");
  }
}
boot();

// 定时器只创建一次，退出登录时由 signOut 清掉；
// 没登录（登录框还盖着）时不打接口，避免无谓请求。
if (!state.timer) {
  state.timer = setInterval(() => {
    if ($("gate").classList.contains("hidden") === false) return;
    if (!state.auto) return;
    if (state.tab === "settings") return;
    loadAll().catch((err) => {
      if (err && err.unauthorized) signOut("登录已失效，请重新登录。");
    });
  }, 15000);
}
