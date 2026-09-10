# CodeBuddy2API

腾讯 CodeBuddy 的轻量 OpenAI 兼容反代。

把多个 CodeBuddy 账号聚合成一个标准 API，可直接给 New API、Cherry Studio 或其他 OpenAI 客户端使用。

## 快速开始

需要 Go 1.25+ 和 gcc（SQLite 走 CGO）。

```bash
git clone https://github.com/<your-name>/codebuddy2api.git
cd codebuddy2api
# 修改 config.yaml 里的 api-key / admin-key
chmod +x run.sh
./run.sh start
```

默认监听 `0.0.0.0:8088`，数据文件在 `data/gateway.db`。

```bash
curl http://127.0.0.1:8088/v1/chat/completions \
  -H "Authorization: Bearer sk-change-me" \
  -H "Content-Type: application/json" \
  -d '{"model":"glm-5.1","stream":true,"messages":[{"role":"user","content":"你好"}]}'
```

健康检查：`GET /healthz`

## 接入 New API

渠道类型选 **OpenAI**：

- Base URL：`http://<host>:8088`
- API Key：`config.yaml` 里的 `gateway.api-key`
- 模型：`glm-5.1`、`glm-5.2`、`hy3` 等，见 `GET /v1/models`

## 导入账号

```bash
curl -X POST http://127.0.0.1:8088/admin/accounts \
  -H "Authorization: Bearer sk-admin-change-me" \
  -H "Content-Type: application/json" \
  -d '{"name":"acc-1","jwt":"<accessToken>","refresh_token":"<refreshToken>"}'
```

批量导入走 `POST /admin/accounts/import`，或：

```bash
python3 scripts/import_accounts.py ./account.json
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
| POST | `/admin/watchdog` | 立刻跑一轮看门狗 |

## Docker

```bash
docker build -t codebuddy2api .
docker run -d --name codebuddy2api -p 8088:8088 \
  -v $PWD/config.yaml:/app/config.yaml \
  -v $PWD/data:/app/data \
  codebuddy2api
```

