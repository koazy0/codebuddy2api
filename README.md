# CodeBuddy2API

腾讯 CodeBuddy 的轻量反代。把桌面端登录态转成本机可直接用的 API。

下游按标准协议调用本服务，本服务再透传到 CodeBuddy。

适用：

- Codex CLI → `POST /v1/responses`
- Claude Code / CC Switch → `POST /v1/messages`
- Cherry Studio / ZCode / LobeChat / NextChat / Open WebUI → `POST /v1/chat/completions`

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

默认监听 `0.0.0.0:8088`。

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
| Codex CLI | `http://<host>:8088/v1` | `POST /v1/responses` |
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

## 导入账号

```bash
python3 scripts/import_accounts.py ./account.json
```

或 `POST /admin/accounts`，Header 用 admin-key。

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
| POST | `/admin/watchdog` | 立刻跑一轮看门狗 |

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
