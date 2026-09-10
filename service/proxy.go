package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"codebuddy-gateway/global"
	"codebuddy-gateway/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type Proxy struct {
	client    *UpstreamClient
	rotator   *Rotator
	refresher *Refresher
}

func NewProxy(client *UpstreamClient, rotator *Rotator, refresher *Refresher) *Proxy {
	return &Proxy{client: client, rotator: rotator, refresher: refresher}
}

type ChatRequestMeta struct {
	RequestedModel string
	UpstreamModel  string
	ClientStream   bool
	Body           []byte
}

func PrepareChatBody(raw []byte) (*ChatRequestMeta, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("invalid json body")
	}
	requested, _ := body["model"].(string)
	if requested == "" {
		return nil, fmt.Errorf("model is required")
	}
	upstream := ResolveModelAlias(requested)
	body["model"] = upstream
	clientStream := true
	if v, ok := body["stream"].(bool); ok {
		clientStream = v
	}
	body["stream"] = true
	if global.CORE_CONFIG.Gateway.InjectReasoning {
		if _, ok := body["reasoningEffort"]; !ok {
			body["reasoningEffort"] = "medium"
		}
		if _, ok := body["reasoning_summary"]; !ok {
			body["reasoning_summary"] = "auto"
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &ChatRequestMeta{
		RequestedModel: requested,
		UpstreamModel:  upstream,
		ClientStream:   clientStream,
		Body:           encoded,
	}, nil
}

func ResolveModelAlias(name string) string {
	for _, alias := range global.CORE_CONFIG.Gateway.ModelAlias {
		if alias.From == name && alias.To != "" {
			return alias.To
		}
	}
	return name
}

func (p *Proxy) HandleChat(c *gin.Context) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		openaiError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	meta, err := PrepareChatBody(raw)
	if err != nil {
		openaiError(c, http.StatusBadRequest, err.Error())
		return
	}
	p.relay(c, meta, "/v2/chat/completions")
}

func (p *Proxy) HandleCompletions(c *gin.Context) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		openaiError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		openaiError(c, http.StatusBadRequest, "invalid json body")
		return
	}
	requested, _ := body["model"].(string)
	if requested == "" {
		requested = "codewise-completions"
	}
	upstream := ResolveModelAlias(requested)
	if upstream == requested {
		upstream = "codewise-completions"
	}
	body["model"] = upstream
	clientStream := true
	if v, ok := body["stream"].(bool); ok {
		clientStream = v
	}
	body["stream"] = true
	encoded, err := json.Marshal(body)
	if err != nil {
		openaiError(c, http.StatusInternalServerError, err.Error())
		return
	}
	p.relay(c, &ChatRequestMeta{
		RequestedModel: requested,
		UpstreamModel:  upstream,
		ClientStream:   clientStream,
		Body:           encoded,
	}, "/v2/completions")
}

func (p *Proxy) relay(c *gin.Context, meta *ChatRequestMeta, path string) {
	start := time.Now()
	exclude := map[uint]struct{}{}
	retries := global.CORE_CONFIG.Gateway.Retries()
	var lastErr string

	for i := 0; i < retries; i++ {
		acc, err := p.rotator.Next(exclude)
		if err != nil {
			lastErr = err.Error()
			break
		}
		exclude[acc.ID] = struct{}{}

		if ShouldRefresh(acc.JWT, time.Hour) {
			if refreshErr := p.refresher.RefreshAccount(c.Request.Context(), acc); refreshErr != nil {
				global.CORE_LOG.Warn("preemptive refresh failed", zap.Uint("account_id", acc.ID), zap.Error(refreshErr))
			}
		}

		resp, err := p.doUpstream(c.Request.Context(), acc, path, meta.Body)
		if err != nil {
			lastErr = err.Error()
			p.rotator.MarkFailure(acc, lastErr)
			continue
		}

		if resp.StatusCode == http.StatusUnauthorized {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if refreshErr := p.refresher.RefreshAccount(c.Request.Context(), acc); refreshErr != nil {
				lastErr = "jwt invalid and refresh failed: " + refreshErr.Error()
				p.rotator.MarkFailure(acc, lastErr)
				continue
			}
			resp, err = p.doUpstream(c.Request.Context(), acc, path, meta.Body)
			if err != nil {
				lastErr = err.Error()
				p.rotator.MarkFailure(acc, lastErr)
				continue
			}
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Sprintf("upstream %d: %s", resp.StatusCode, clip(raw, 200))
			p.rotator.MarkFailure(acc, lastErr)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Sprintf("upstream %d: %s", resp.StatusCode, clip(raw, 200))
			p.rotator.MarkFailure(acc, lastErr)
			if resp.StatusCode >= 500 {
				continue
			}
			openaiError(c, http.StatusBadGateway, lastErr)
			p.recordUsage(acc, meta, start, resp.StatusCode, lastErr, nil)
			return
		}

		p.rotator.MarkSuccess(acc)
		usage, writeErr := p.writeResponse(c, resp, meta)
		if writeErr != nil {
			global.CORE_LOG.Warn("write upstream response failed", zap.Error(writeErr))
		}
		p.recordUsage(acc, meta, start, http.StatusOK, "", usage)
		return
	}

	if lastErr == "" {
		lastErr = "no available codebuddy account"
	}
	openaiError(c, http.StatusServiceUnavailable, lastErr)
}

func (p *Proxy) doUpstream(ctx context.Context, acc *model.Account, path string, body []byte) (*http.Response, error) {
	if path == "/v2/completions" {
		return p.client.Completions(ctx, acc, body)
	}
	return p.client.ChatCompletions(ctx, acc, body)
}

func (p *Proxy) writeResponse(c *gin.Context, resp *http.Response, meta *ChatRequestMeta) (*parsedUsage, error) {
	defer resp.Body.Close()
	if meta.ClientStream {
		return p.writeStream(c, resp, meta)
	}
	return p.writeJSON(c, resp, meta)
}

func (p *Proxy) writeStream(c *gin.Context, resp *http.Response, meta *ChatRequestMeta) (*parsedUsage, error) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	streamStart := time.Now()
	var usage *parsedUsage
	var firstTokenMs int64
	reqID := resp.Header.Get("X-Request-Id")
	for scanner.Scan() {
		line := scanner.Text()
		if firstTokenMs == 0 && lineHasGeneratedToken(line) {
			firstTokenMs = time.Since(streamStart).Milliseconds()
		}
		out, parsed := rewriteSSELine(line, meta.RequestedModel)
		usage = mergeUsage(usage, parsed)
		if _, err := io.WriteString(c.Writer, out+"\n"); err != nil {
			if usage != nil {
				usage.FirstTokenMs = firstTokenMs
			}
			return usage, err
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if usage == nil {
		usage = &parsedUsage{}
	}
	usage.FirstTokenMs = firstTokenMs
	if reqID != "" {
		usage.RequestID = reqID
	}
	if err := scanner.Err(); err != nil {
		return usage, err
	}
	return usage, nil
}

func (p *Proxy) writeJSON(c *gin.Context, resp *http.Response, meta *ChatRequestMeta) (*parsedUsage, error) {
	agg, usage, err := aggregateSSE(resp.Body, meta.RequestedModel)
	if err != nil {
		return nil, err
	}
	if usage != nil && usage.RequestID == "" {
		usage.RequestID = resp.Header.Get("X-Request-Id")
	}
	c.Header("Content-Type", "application/json")
	c.Status(http.StatusOK)
	_, writeErr := c.Writer.Write(agg)
	return usage, writeErr
}

func rewriteSSELine(line, requestedModel string) (string, *parsedUsage) {
	if !strings.HasPrefix(line, "data: ") {
		return line, nil
	}
	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" {
		return line, nil
	}
	var chunk map[string]any
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return line, nil
	}
	if requestedModel != "" {
		chunk["model"] = requestedModel
	}
	if !global.CORE_CONFIG.Gateway.Passthrough {
		stripNonOpenAI(chunk)
	}
	usage := extractUsage(chunk)
	encoded, err := json.Marshal(chunk)
	if err != nil {
		return line, usage
	}
	return "data: " + string(encoded), usage
}

func aggregateSSE(r io.Reader, requestedModel string) ([]byte, *parsedUsage, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var id, modelName, finishReason string
	var content bytes.Buffer
	var reasoning bytes.Buffer
	var usage *parsedUsage
	created := time.Now().Unix()
	streamStart := time.Now()
	var firstTokenMs int64
	for scanner.Scan() {
		line := scanner.Text()
		if firstTokenMs == 0 && lineHasGeneratedToken(line) {
			firstTokenMs = time.Since(streamStart).Milliseconds()
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if v, ok := chunk["id"].(string); ok && v != "" {
			id = v
		}
		if v, ok := chunk["model"].(string); ok && v != "" {
			modelName = v
		}
		if v, ok := chunk["created"].(float64); ok && v > 0 {
			created = int64(v)
		}
		usage = mergeUsage(usage, extractUsage(chunk))
		choices, _ := chunk["choices"].([]any)
		for _, item := range choices {
			choice, _ := item.(map[string]any)
			if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
				finishReason = fr
			}
			delta, _ := choice["delta"].(map[string]any)
			if delta == nil {
				if msg, ok := choice["message"].(map[string]any); ok {
					delta = msg
				}
			}
			if delta == nil {
				continue
			}
			if v, ok := delta["content"].(string); ok {
				content.WriteString(v)
			}
			if v, ok := delta["reasoning_content"].(string); ok {
				reasoning.WriteString(v)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, usage, err
	}
	if requestedModel != "" {
		modelName = requestedModel
	}
	if finishReason == "" {
		finishReason = "stop"
	}
	if usage == nil {
		usage = &parsedUsage{}
	}
	usage.FirstTokenMs = firstTokenMs
	if id != "" {
		usage.RequestID = id
	}
	message := map[string]any{
		"role":    "assistant",
		"content": content.String(),
	}
	if global.CORE_CONFIG.Gateway.Passthrough && reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	usageObj := map[string]any{
		"prompt_tokens":     usage.PromptTokens,
		"completion_tokens": usage.CompletionTokens,
		"total_tokens":      usage.TotalTokens,
	}
	if global.CORE_CONFIG.Gateway.Passthrough {
		usageObj["credit"] = usage.Credit
		usageObj["prompt_cache_hit_tokens"] = usage.CacheHitTokens
		usageObj["prompt_cache_miss_tokens"] = usage.CacheMissTokens
		usageObj["completion_thinking_tokens"] = usage.ThinkingTokens
	}
	resp := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   modelName,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       message,
				"finish_reason": finishReason,
			},
		},
		"usage": usageObj,
	}
	encoded, err := json.Marshal(resp)
	return encoded, usage, err
}

func stripNonOpenAI(chunk map[string]any) {
	if choices, ok := chunk["choices"].([]any); ok {
		for _, item := range choices {
			choice, _ := item.(map[string]any)
			if choice == nil {
				continue
			}
			if delta, ok := choice["delta"].(map[string]any); ok {
				delete(delta, "reasoning_content")
				delete(delta, "refusal")
				delete(delta, "extra_fields")
				if fc, ok := delta["function_call"].(map[string]any); ok {
					name, _ := fc["name"].(string)
					args, _ := fc["arguments"].(string)
					if name == "" && args == "" {
						delete(delta, "function_call")
					}
				}
			}
		}
	}
	if usage, ok := chunk["usage"].(map[string]any); ok {
		for _, key := range []string{
			"credit", "prompt_cache_hit_tokens", "prompt_cache_miss_tokens",
			"cache_read_input_tokens", "cache_creation_input_tokens",
			"prompt_cache_write_tokens", "completion_thinking_tokens",
			"cached_tokens", "prompt_tokens_details", "completion_tokens_details",
		} {
			delete(usage, key)
		}
	}
}

func (p *Proxy) recordUsage(acc *model.Account, meta *ChatRequestMeta, start time.Time, status int, errMsg string, usage *parsedUsage) {
	if usage == nil {
		usage = &parsedUsage{}
	}
	deriveMetrics(usage, time.Since(start).Milliseconds())
	log := &model.UsageLog{
		AccountID:                acc.ID,
		Model:                    meta.RequestedModel,
		UpstreamModel:            meta.UpstreamModel,
		Stream:                   meta.ClientStream,
		PromptTokens:             usage.PromptTokens,
		CompletionTokens:         usage.CompletionTokens,
		TotalTokens:              usage.TotalTokens,
		ThinkingTokens:           usage.ThinkingTokens,
		Credit:                   usage.Credit,
		EstimatedCredit:          estimateCredit(usage.PromptTokens, usage.CompletionTokens),
		CacheHitTokens:           usage.CacheHitTokens,
		CacheMissTokens:          usage.CacheMissTokens,
		CacheHitRate:             usage.CacheHitRate,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheWriteTokens:         usage.CacheWriteTokens,
		CachedTokens:             usage.CachedTokens,
		FirstTokenMs:             usage.FirstTokenMs,
		LatencyMs:                usage.LatencyMs,
		TokensPerSecond:          usage.TokensPerSecond,
		OutputTokensPerSecond:    usage.OutputTokensPerSecond,
		StatusCode:               status,
		Error:                    errMsg,
		RequestID:                usage.RequestID,
	}
	if usage.Credit > 0 {
		if deduct, err := model.ApplyCreditDeduction(acc.ID, usage.Credit); err == nil && deduct != nil {
			log.CreditMonthly = deduct.Monthly
			log.CreditOnetime = deduct.Onetime
			log.CreditSource = deduct.Source
			if deduct.Remain <= 0 && acc.CreditSyncedAt != nil {
				until := time.Now().Add(time.Duration(global.CORE_CONFIG.Watchdog.Cooldown()) * time.Second)
				_ = model.MarkAccountFailure(acc.ID, "credit exhausted", acc.FailCount+1, model.AccountStatusCooldown, &until)
			}
		}
	}
	_ = model.CreateUsageLog(log)
}

func openaiError(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": msg,
			"type":    "codebuddy_gateway_error",
		},
	})
}
