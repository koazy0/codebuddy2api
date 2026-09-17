# CodeBuddy2API

腾讯 CodeBuddy 的轻量反代。把桌面端登录态转成本机可直接用的 API。

下游按标准协议调用本服务，本服务再透传到 CodeBuddy。

适用：

- Codex CLI → `POST /v1/responses`
- Claude Code / CC Switch → `POST /v1/messages`
- Cherry Studio / ZCode / LobeChat / NextChat / Open WebUI → `POST /v1/chat/completions`

## 特色

单二进制 Go 网关，不做用户系统、不做计费面板。核心是把 CodeBuddy 登录态转成标准 API，并把账号、票据、额度自己养起来。

- **Codex 原生兼容**（本项目最大差异点）：Codex CLI 不是简单的 Chat Completions 客户端。它会带超长系统提示、`developer` 角色、`namespace` / `custom` 工具（`exec` grammar、`multi_agent_v1`、`apply_patch`）。本网关会在出站前把这些收成 CodeBuddy 吃得下的 Chat Completions：清洗掉会触发 WAF（`11128`）的 harness 文本（system/developer 品牌指纹、harness 注入的 user 上下文、tool 描述，`full` 档再加 assistant 历史与 tool 输出），用户真实提问与 Codex 身份、工作方式原样透传；把 namespace / custom 工具展开成标准 function；`developer` 映射为 `system`；WAF 拒绝时把请求清得更干净重发一次，而不是换号或冷却账号。效果是 Codex 能真正 `exec_command`、改文件、派子 agent，体感仍是原生 Codex。
- **协议兼容**：`/v1/chat/completions`、`/v1/responses`、`/v1/messages`，工具调用一起转，Codex / Claude Code / Cherry Studio 直接接。
- **实时模型目录**：`GET /v1/models` 透传上游 `/v3/config`，不是本地写死的名单。
- **多账号轮换**：`round_robin` / `least_used` / `sticky`，额度耗尽自动跳过，失败按 `max-retries` 换号重试。
- **按实测成本选号**：上游对不同账号在同一模型上的计费并不一致（实测同名模型跨账号单价可差数十倍），而是否收费只能从响应里的 `usage.credit` 观察得到——限免、夜间免费、试用期都只体现在这里。网关按 `(账号, 模型)` 记录实测单价并分层，**优先挑免费号**：有免费号就只用免费号，没有就退到尚未观测的号（其中可能藏着还没发现是免费的）。观测 6 小时过期，避免夜间免费的号白天仍被当成免费。`GET /admin/cost-ledger` 可查看账本规模。
- **积分任务中心**：一键完成成长中心的任务并自动领奖。任务是靠**行为事件**计分而非 UI 操作，所以实现走的是「报名 → 上报事件链 → 等计分 → 领奖」四阶段，全程纯 API、幂等，重复点不会重复加分。实测单个新账号可完成 15+ 个任务、到账约 1550 分 / 70 能量。
- **官方登录入库**：`auth login` 走 `POST /v2/plugin/auth/state`，再轮询 `GET /v2/plugin/auth/token`；JSON 导入按 jwt / username upsert。
- **刷新票据**：`POST /v2/plugin/auth/token/refresh`。默认每天 03:00 扫描，JWT 剩余不足 30 天就续；请求前也会预刷新。
- **看门狗**：默认 300 秒一轮。健康检查、同步额度、连续失败 3 次进冷却，冷却到期自动重新启用。
- **额度账本**：每月再生额度优先，再用一次性额度；看门狗定期从上游修正余额，每次对话同时记 token 和积分。
- **用量统计**：输入/输出 token、缓存命中、首 token、延迟、TPS、额度来源，走 `/admin/usage` 和 `/admin/usage/summary`。
- **本机控制台**：打开 `http://<host>:8088/` 就能看账号、余额、每次请求走了哪张票据、使用记录，以及上次/下次票据刷新时间。刷新 cron 也可以在网页上改，保存后热更新并写回 `config.yaml`。
- **存储可选**：默认 SQLite，可切 MySQL / PostgreSQL。

## 快速开始

需要 Go 1.25+ 和 gcc（SQLite 走 CGO）。

```bash
git clone https://github.com/koazy0/codebuddy2api.git
cd codebuddy2api
cp .env.example .env
# 修改 config.yaml 或 .env 里的 API Key
chmod +x run.sh
./run.sh start
```

默认监听 `0.0.0.0:8088`。浏览器打开 `http://127.0.0.1:8088/`，用 admin key 进入控制台。

## 下游 API Key

三种方式都能配，优先级：

**命令行 > 环境变量 / `.env` > `config.yaml`**

`config.yaml`：

```yaml
gateway:
  api-key: "sk-your-key"
  admin-key: "sk-admin-your-key"
```

`.env` 或环境变量：

```bash
GATEWAY_API_KEY=sk-your-key
GATEWAY_ADMIN_KEY=sk-admin-your-key
```

命令行：

```bash
./codebuddy-gateway server --api-key sk-your-key --admin-key sk-admin-your-key
```

## 下游怎么接

Header 用 `Authorization: Bearer <api-key>`，也认 `api-key` / `X-Api-Key`。

| 客户端 | Base URL | 协议 |
|------|----------|------|
| Cherry Studio / New API / Open WebUI | `http://<host>:8088` 或 `http://<host>:8088/v1` | `POST /v1/chat/completions` |
| Codex CLI | `http://<host>:8088/v1` | `POST /v1/responses`（见下方专节） |
| Claude Code / CC Switch | `http://<host>:8088` | `POST /v1/messages` |

模型列表：`GET /v1/models`（透传上游 `GET /v3/config`）。

```bash
# OpenAI Chat Completions
curl http://127.0.0.1:8088/v1/chat/completions \
  -H "Authorization: Bearer sk-your-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"glm-5.2","stream":true,"messages":[{"role":"user","content":"你好"}]}'

# OpenAI Responses（Codex CLI）
curl http://127.0.0.1:8088/v1/responses \
  -H "Authorization: Bearer sk-your-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"glm-5.2","stream":true,"input":"你好"}'

# Anthropic Messages（Claude Code）
curl http://127.0.0.1:8088/v1/messages \
  -H "x-api-key: sk-your-key" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"glm-5.2","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"你好"}]}'
```

健康检查：`GET /healthz`（无需 Key）。

## Codex CLI

Codex 走 `wire_api = "responses"`。网关会把请求收成上游 `/v2/chat/completions`，并专门处理 Codex 才会带的结构：

- 出站前做请求清洗，压掉会触发上游 WAF（`11128`）的 harness 文本。**默认配置即最优，装上就能用，写代码不被打断**。清洗只改客户端模板，**用户真实提问逐字节保留**；`assistant` / `tool` 这类对话记录只做不可见脱敏（零宽），绝不删改文字。力度由 `gateway.sanitize-mode` 控制：
  - `harness`（默认）— 覆盖全部常见触发面：system/developer 文本的品牌指纹与合规词、harness 注入的 user 上下文、tool 定义的 `description`，以及随会话累积的 assistant 历史与 tool 输出。后两者正是长会话「跑一会儿才断」的成因，放在默认档处理才不会让你感知到中途失败。
  - `full` — 额外允许删除被污染的模板文本（句子级剪枝）。被 11128 拒绝时自动升到这一档。
  - `off` — 关闭清洗，仅用于排障对比。
- 出站统一剥离零宽标记：脱敏用的不可见字符绝不会随响应流回客户端，避免被写进你的源文件（肉眼看不见，但 diff 会显示有改动、字符串比较会失败）。
- 被 `11128` 拒绝时自动升到 `full` 档重发一次，且**不排除当前账号**——拦的是请求内容不是账号，换号只会把整池烧一遍。若该趟清无可清则直接返回错误，不做无意义的循环。
- tool 定义的 `name` / `enum` / `default` / `parameters` 属功能字段，任何档位都不改写；apply_patch 的 `*** Begin Patch` 格式说明也始终保留，避免把「请求被拒」换成「工具调用崩掉」。
- `namespace` 工具展成 `multi_agent_v1__spawn_agent` 这类 function；`custom` 工具（如 `exec`、`apply_patch`）收成带 `cmd` / `input` 的 function。只丢 `web_search` 这类上游没有的类型。
- 上游 `11128` / `11102` 只记日志，不冷却账号。

`~/.codex/config.toml` 示例：

```toml
model_provider = "codebuddy2api"
model = "deepseek-v4.1-flash"
model_context_window = 1000000
model_max_output_tokens = 32000

[model_providers.codebuddy2api]
name = "codebuddy2api"
base_url = "http://127.0.0.1:8088/v1"
env_key = "CODEBUDDY2API_KEY"
experimental_bearer_token = "sk-your-key"
wire_api = "responses"
requires_openai_auth = false
```

`GET /v1/models` 能看到当前账号可用的模型，按需把 `model` 换成 `glm-5.2`、`kimi-k2.7` 等。自定义 `model_catalog_json` 时不要照抄 GPT-5.6 的 `tool_mode = code_mode_only`，否则 Codex 会改成 custom `exec` grammar，工具列表只剩 `wait`。

## 导入账号

上游用的是 CodeBuddy 登录态，不是 OpenAI Key。用 CLI 拿凭证并入库即可，不必先启动服务。

### 网页 / 扫码登录

```bash
./codebuddy-gateway auth login
```

流程：

1. 向 CodeBuddy 申请登录链接：`POST /v2/plugin/auth/state`
2. 浏览器打开链接，完成登录
3. 轮询 `GET /v2/plugin/auth/token` 拿到 `accessToken` / `refreshToken`
4. 写入本地数据库

常用参数：

```bash
./codebuddy-gateway auth login --no-browser          # 只打印链接
./codebuddy-gateway auth login --out creds.json      # 同时保存 JSON
./codebuddy-gateway auth login --no-save             # 只拿 token，不入库
./codebuddy-gateway auth login --platform desktop    # 默认 desktop，也可 CLI
```

### 从 JSON 导入

桌面端导出的 JSON，或上一步 `--out` 的文件：

```bash
./codebuddy-gateway account import ./account.json
./codebuddy-gateway account import ./dir-of-json --dry-run
./codebuddy-gateway account list
```

也兼容原来的脚本：`python3 scripts/import_accounts.py ./account.json`。
管理接口 `POST /admin/accounts/import` 仍然可用。

凭证入库后由刷新任务和看门狗继续维护，不必每次请求前手动续。

## 定时维护

登录拿到的 token 不会一直有效。打开 `refresh` 和 `watchdog` 后，网关自己续票据、对余额、把坏号冷却。

```yaml
refresh:
    enabled: true
    cron: "0 3 * * *"
    threshold-days: 30

watchdog:
    enabled: true
    interval-seconds: 300
    fail-threshold: 3
    cooldown-seconds: 600
    health-check: true
    sync-credit: true
```

也可手动触发：

```bash
curl -H "Authorization: Bearer sk-admin-your-key" -X POST http://127.0.0.1:8088/admin/refresh
curl -H "Authorization: Bearer sk-admin-your-key" -X POST http://127.0.0.1:8088/admin/sync-credit
curl -H "Authorization: Bearer sk-admin-your-key" -X POST http://127.0.0.1:8088/admin/watchdog
```

## 管理接口

均需 `Authorization: Bearer <admin-key>`。

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/admin/accounts` | 账号列表 |
| POST | `/admin/accounts` | 新增账号 |
| POST | `/admin/accounts/import` | 批量导入 |
| PUT | `/admin/accounts/:id` | 更新 |
| DELETE | `/admin/accounts/:id` | 删除 |
| POST | `/admin/accounts/:id/refresh` | 刷新凭证 |
| POST | `/admin/accounts/:id/sync-credit` | 同步额度 |
| POST | `/admin/accounts/:id/enable` | 启用 |
| POST | `/admin/accounts/:id/disable` | 停用 |
| POST | `/admin/refresh` | 扫描刷新即将过期账号 |
| POST | `/admin/sync-credit` | 全量同步额度 |
| GET | `/admin/models` | 模型目录 |
| PUT | `/admin/models` | 新增/更新模型 |
| GET | `/admin/usage` | 用量明细 |
| GET | `/admin/usage/summary` | 用量汇总 |
| GET | `/admin/settings/refresh` | 查看票据刷新计划 |
| PUT | `/admin/settings/refresh` | 修改刷新 cron / 阈值，热更新并写回配置 |
| GET | `/admin/settings/access` | 面板登录设置 |
| PUT | `/admin/settings/access` | 修改面板标题 / 密码 |
| POST | `/admin/auth/verify` | 控制台登录校验（无需先带 admin key） |
| GET | `/admin/stats/daily` | 每日 token 消耗 |
| GET | `/admin/stats/models` | 模型消耗排行 |
| GET | `/admin/stats/accounts` | 账号消耗排行 |
| POST | `/admin/watchdog` | 立刻跑一轮看门狗 |
| GET | `/admin/accounts/:id/tasks` | 成长中心任务列表（进度 / 奖励 / 状态） |
| POST | `/admin/accounts/:id/tasks/run` | 一键完成：报名 → 上报事件 → 等计分 → 自动领奖。body `{"codes":[...]}` 可只跑指定任务 |
| POST | `/admin/accounts/:id/tasks/accept` | 批量报名全部未接受任务 |
| POST | `/admin/accounts/:id/tasks/:code/claim` | 单独领取某任务奖励 |
| GET | `/admin/tasks/catalog` | 可自动化任务清单 |
| POST | `/admin/tasks/batch` | **批量做任务**：对全部未停用账号启动一轮，body `{"codes":[...],"concurrency":3}` |

### 积分任务说明

任务进度由上游按**行为事件**异步聚合，不是同步返回的。所以「一键完成」的流程是：先批量报名（未报名时 `target` 恒为 0，上报不计数），再按依赖顺序上报各任务的事件链，等约 3 秒让计分落定，最后对达标任务自动领奖。全程幂等，重复执行不会重复加分。

面板上账号行的「积分任务」按钮做单个账号；工具栏的「批量做任务」按钮做全部账号。

批量是**异步**的：接口只启动并立刻返回，实际执行在后台，前端每 3 秒轮询 `GET /admin/tasks/batch` 看进度。之所以不做成同步接口——单账号要 1-2 分钟，多账号串在一个 HTTP 请求里会被反向代理或隧道按空闲超时掐断。同一时刻只允许一批，重复启动会被拒，避免同一账号被两轮抢着上报事件。

已知两项无法自动完成：`Expert_Philanthropy` 没有可用进度判据；`black_cat`（夜猫子）只在 23:00–08:00 窗口内计分。

## Docker

```bash
docker build -t codebuddy2api .
docker run -d --name codebuddy2api -p 8088:8088 \
  -e GATEWAY_API_KEY=sk-your-key \
  -e GATEWAY_ADMIN_KEY=sk-admin-your-key \
  -v $PWD/config.yaml:/app/config.yaml \
  -v $PWD/data:/app/data \
  codebuddy2api
```
