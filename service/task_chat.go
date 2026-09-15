package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"codebuddy-gateway/model"
)

// ---------------------------------------------------------------------------
// 任务链路用到的上游原语：真实对话、领养、协议
// ---------------------------------------------------------------------------

// realChat 发一次真实对话（上游 /v2/chat/completions），返回服务端
// conversationId / requestId。
//
// 为什么必须走真实对话而非只发事件：expert_5、Expert_team_use_3、
// skill_1、Expert_lighthouse 的判据要求事件里的 requestId **对齐服务端真实
// requestId**，自造的 ID 不计数。所以这些任务必须先真打一次 chat 拿到 ID。
//
// 上游返回 SSE，这里只读响应头就够（ID 在头里），但 body 必须读干——
// 不读完会残留连接，影响后续请求。
func (c *UpstreamClient) realChat(acc *model.Account, modelID, prompt string) (conversationID, requestID string, err error) {
	conversationID = fmt.Sprintf("cbgw-conv-%d", time.Now().UnixNano())
	body := map[string]any{
		"model": modelID,
		"messages": []any{
			map[string]any{"role": "system", "content": "You are a helpful assistant. 当前处于中文环境，使用简体中文回答。"},
			map[string]any{"role": "user", "content": prompt},
		},
		"agent":          "cli",
		"temperature":    1,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequest(http.MethodPost, growthBase()+"/v2/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", "", err
	}
	growthHeaders(req, acc)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Conversation-ID", conversationID)
	req.Header.Set("X-Request-ID", fmt.Sprintf("%d", time.Now().UnixNano()))
	req.Header.Set("X-Agent-Intent", "craft")
	req.Header.Set("X-Agent-Type", "main")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	// 服务端会在响应头回带真实 ID；缺失时回落到本地生成的（用于事件 JOIN）。
	requestID = firstNonEmpty(
		resp.Header.Get("X-Request-Id"),
		resp.Header.Get("X-Request-ID"),
		resp.Header.Get("x-request-id"),
	)
	if v := resp.Header.Get("X-Conversation-Id"); v != "" {
		conversationID = v
	}
	if resp.StatusCode >= 400 {
		rawErr, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", "", fmt.Errorf("chat http %d: %s", resp.StatusCode, clip(rawErr, 200))
	}
	// 读干响应体（SSE），避免连接残留。
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if requestID == "" {
		requestID = conversationID
	}
	return conversationID, requestID, nil
}

// buddyAgreement 同意 Buddy 用户协议（幂等）。
func (c *UpstreamClient) buddyAgreement(acc *model.Account) error {
	raw, err := json.Marshal(map[string]any{"agreed": true})
	if err != nil {
		return err
	}
	req, err := newJSONPost(growthBase()+"/v2/buddy/agreement", raw)
	if err != nil {
		return err
	}
	growthHeaders(req, acc)
	return c.doGrowth(req)
}

// buddyFirst 领取第一只 Buddy（+300 分）。
func (c *UpstreamClient) buddyFirst(acc *model.Account) error {
	req, err := newJSONPost(growthBase()+"/v2/buddy/first", []byte("{}"))
	if err != nil {
		return err
	}
	growthHeaders(req, acc)
	return c.doGrowth(req)
}
