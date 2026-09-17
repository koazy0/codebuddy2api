package service

import (
	"bufio"
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
	Protocol       Protocol
	RequestedModel string
	UpstreamModel  string
	ClientStream   bool
	Body           []byte
	ClientIP       string
	UserAgent      string
	RequestPreview string
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
	sanitizeUpstreamChatWithMode(body, global.CORE_CONFIG.Gateway.SanitizeModeName())
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &ChatRequestMeta{
		Protocol:       ProtocolChat,
		RequestedModel: requested,
		UpstreamModel:  upstream,
		ClientStream:   clientStream,
		Body:           encoded,
		RequestPreview: captureChatRequest(encoded),
	}, nil
}

func (p *Proxy) HandleChat(c *gin.Context) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		gatewayError(c, ProtocolChat, http.StatusBadRequest, "invalid request body")
		return
	}
	meta, err := PrepareChatBody(raw)
	if err != nil {
		gatewayError(c, ProtocolChat, http.StatusBadRequest, err.Error())
		return
	}
	attachClientMeta(c, meta)
	p.relay(c, meta, "/v2/chat/completions")
}

func (p *Proxy) HandleResponses(c *gin.Context) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		gatewayError(c, ProtocolResponses, http.StatusBadRequest, "invalid request body")
		return
	}
	meta, err := PrepareResponsesBody(raw)
	if err != nil {
		gatewayError(c, ProtocolResponses, http.StatusBadRequest, err.Error())
		return
	}
	attachClientMeta(c, meta)
	p.relay(c, meta, "/v2/chat/completions")
}

func (p *Proxy) HandleMessages(c *gin.Context) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		gatewayError(c, ProtocolAnthropic, http.StatusBadRequest, "invalid request body")
		return
	}
	meta, err := PrepareAnthropicBody(raw)
	if err != nil {
		gatewayError(c, ProtocolAnthropic, http.StatusBadRequest, err.Error())
		return
	}
	attachClientMeta(c, meta)
	p.relay(c, meta, "/v2/chat/completions")
}

func (p *Proxy) HandleCountTokens(c *gin.Context) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		gatewayError(c, ProtocolAnthropic, http.StatusBadRequest, "invalid request body")
		return
	}
	c.Header("anthropic-version", "2023-06-01")
	c.JSON(http.StatusOK, gin.H{"input_tokens": estimateTokenCount(raw)})
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
	meta := &ChatRequestMeta{
		Protocol:       ProtocolChat,
		RequestedModel: requested,
		UpstreamModel:  upstream,
		ClientStream:   clientStream,
		Body:           encoded,
	}
	attachClientMeta(c, meta)
	p.relay(c, meta, "/v2/completions")
}

func (p *Proxy) relay(c *gin.Context, meta *ChatRequestMeta, path string) {
	start := time.Now()
	exclude := map[uint]struct{}{}
	retries := global.CORE_CONFIG.Gateway.Retries()
	var lastErr string
	// wafRetried：11128 被安全策略拒绝后，我们已经把请求体换成更干净的版本重发过一次。
	// 只做一轮：换账号对 11128 无效（日志已证明换遍全池仍被拦），能救的只有改请求内容。
	wafRetried := false

	for i := 0; i < retries; i++ {
		acc, err := p.rotator.NextFor(exclude, meta.UpstreamModel)
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
			if isModelQuotaExhausted(resp.StatusCode, raw) {
				p.rotator.MarkModelExhausted(acc, meta.UpstreamModel, lastErr)
			} else {
				p.rotator.MarkFailure(acc, lastErr)
			}
			continue
		}

		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Sprintf("upstream %d: %s", resp.StatusCode, clip(raw, 200))
			if isUpstreamModelUnavailable(raw) && maybeRewriteUnavailableModel(meta) {
				global.CORE_LOG.Warn("upstream model unavailable, retrying same account with fallback",
					zap.Uint("account_id", acc.ID),
					zap.String("error", lastErr),
					zap.String("fallback", meta.UpstreamModel))
				resp, err = p.doUpstream(c.Request.Context(), acc, path, meta.Body)
				if err != nil {
					lastErr = err.Error()
					continue
				}
				if resp.StatusCode == http.StatusOK {
					p.commitSuccess(c, acc, resp, meta, start)
					return
				}
				raw, _ = io.ReadAll(resp.Body)
				resp.Body.Close()
				lastErr = fmt.Sprintf("upstream %d: %s", resp.StatusCode, clip(raw, 200))
			}
			if isModelQuotaExhausted(resp.StatusCode, raw) {
				p.rotator.MarkModelExhausted(acc, meta.UpstreamModel, lastErr)
				continue
			}
			if isUnapprovedChannel(raw) && !wafRetried {
				// 安全策略拦的是「请求内容」，不是账号：换账号重试只会把整池烧一遍
				// （线上日志实测：同一请求连续换 8 个账号，全部 11128）。
				// 正确做法是就地降级——把 harness 注入的 user 消息也一并脱敏，重发一次。
				wafRetried = true
				global.CORE_LOG.Warn("upstream blocked by security policy, escalating sanitize and retrying",
					zap.Uint("account_id", acc.ID), zap.String("error", lastErr))
				if retryWAFRejectedBody(meta) {
					// 不排除当前账号：账号本身没问题，换号解决不了内容问题。
					delete(exclude, acc.ID)
					i--
					continue
				}
				global.CORE_LOG.Warn("escalating sanitize produced no change, giving up", zap.Uint("account_id", acc.ID))
			}
			if isUpstreamRequestError(raw) {
				global.CORE_LOG.Warn("upstream rejected request without burning account", zap.Uint("account_id", acc.ID), zap.String("error", lastErr))
			} else {
				p.rotator.MarkFailure(acc, lastErr)
			}
			if resp.StatusCode >= 500 {
				continue
			}
			status := http.StatusBadGateway
			if isUpstreamRequestError(raw) {
				status = http.StatusBadRequest
			}
			gatewayError(c, meta.Protocol, status, lastErr)
			p.recordUsage(acc, meta, start, resp.StatusCode, lastErr, nil)
			return
		}

		p.commitSuccess(c, acc, resp, meta, start)
		return
	}

	if lastErr == "" {
		lastErr = "no available codebuddy account"
	}
	gatewayError(c, meta.Protocol, http.StatusServiceUnavailable, lastErr)
}

func (p *Proxy) commitSuccess(c *gin.Context, acc *model.Account, resp *http.Response, meta *ChatRequestMeta, start time.Time) {
	p.rotator.MarkSuccessFor(acc, meta.UpstreamModel)
	usage, writeErr := p.writeResponse(c, resp, meta)
	if writeErr != nil {
		global.CORE_LOG.Warn("write upstream response failed", zap.Error(writeErr))
	}
	p.recordUsage(acc, meta, start, http.StatusOK, "", usage)
}

func (p *Proxy) doUpstream(ctx context.Context, acc *model.Account, path string, body []byte) (*http.Response, error) {
	if path == "/v2/completions" {
		return p.client.Completions(ctx, acc, body)
	}
	return p.client.ChatCompletions(ctx, acc, body)
}

func (p *Proxy) writeResponse(c *gin.Context, resp *http.Response, meta *ChatRequestMeta) (*parsedUsage, error) {
	defer resp.Body.Close()
	if meta.Protocol == ProtocolResponses || meta.Protocol == ProtocolAnthropic {
		if meta.ClientStream {
			return p.writeCompatStream(c, resp, meta)
		}
		return p.writeCompatJSON(c, resp, meta)
	}
	if meta.ClientStream {
		return p.writeStream(c, resp, meta)
	}
	return p.writeJSON(c, resp, meta)
}

func (p *Proxy) writeStream(c *gin.Context, resp *http.Response, meta *ChatRequestMeta) (*parsedUsage, error) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	sink := newSSESink(c.Writer, flusher)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	streamStart := time.Now()
	var usage *parsedUsage
	var firstTokenMs int64
	collector := NewCaptureCollector()
	reqID := resp.Header.Get("X-Request-Id")

	// 上游静默期间补 SSE ping 事件，同时喂饱代理空闲超时和 Codex idle timeout。
	stopHeartbeat := startSSEHeartbeat(sink)
	defer stopHeartbeat()

	for scanner.Scan() {
		line := scanner.Text()
		if firstTokenMs == 0 && lineHasGeneratedToken(line) {
			firstTokenMs = time.Since(streamStart).Milliseconds()
		}
		out, parsed := rewriteSSELine(line, meta.RequestedModel)
		if parsed == nil {
			parsed = &parsedUsage{}
		}
		parsed.Collector = collector
		collector.Write(chunkText(line))
		usage = mergeUsage(usage, parsed)
		if _, err := io.WriteString(sink, out+"\n"); err != nil {
			if usage != nil {
				usage.FirstTokenMs = firstTokenMs
			}
			return usage, err
		}
	}
	if usage == nil {
		usage = &parsedUsage{}
	}
	usage.Collector = collector
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
	result, err := collectSSE(resp.Body, meta.RequestedModel)
	if err != nil {
		return nil, err
	}
	if result.Usage == nil {
		result.Usage = &parsedUsage{}
	}
	if result.Usage.RequestID == "" {
		result.Usage.RequestID = resp.Header.Get("X-Request-Id")
	}
	collector := NewCaptureCollector()
	collector.Write(jsonResponseText(result))
	result.Usage.Collector = collector
	agg, err := encodeChatJSON(result)
	if err != nil {
		return result.Usage, err
	}
	c.Header("Content-Type", "application/json")
	c.Status(http.StatusOK)
	_, writeErr := c.Writer.Write(stripInvisibleFromFrame(agg))
	return result.Usage, writeErr
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
	result, err := collectSSE(r, requestedModel)
	if err != nil {
		return nil, nil, err
	}
	encoded, err := encodeChatJSON(result)
	if err != nil {
		return nil, result.Usage, err
	}
	return encoded, result.Usage, nil
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
		delete(usage, "credit")
	}
}

func attachClientMeta(c *gin.Context, meta *ChatRequestMeta) {
	if meta == nil {
		return
	}
	meta.ClientIP = c.ClientIP()
	meta.UserAgent = clipText(c.Request.UserAgent(), 240)
}

func (p *Proxy) recordUsage(acc *model.Account, meta *ChatRequestMeta, start time.Time, status int, errMsg string, usage *parsedUsage) {
	if usage == nil {
		usage = &parsedUsage{}
	}
	if status == http.StatusOK {
		rememberGoodModel(meta.UpstreamModel)
	}
	deriveMetrics(usage, time.Since(start).Milliseconds())
	log := &model.UsageLog{
		AccountID:                acc.ID,
		AccountName:              accountLabel(acc),
		Protocol:                 string(meta.Protocol),
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
		ClientIP:                 meta.ClientIP,
		UserAgent:                meta.UserAgent,
		RequestTokens:            len(meta.Body),
		RequestPreview:           meta.RequestPreview,
		ResponseTokens:           usage.ResponseBytes,
		ResponsePreview:          usage.ResponsePreview,
		RawUsage:                 clipRawUsage(usage.Raw),
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
	// 成本账本：记录该账号跑该模型的实测单价，供下次选号优先挑免费的。
	//
	// 只记上游真回了 usage 的请求：tokens 为 0 时可能是响应缺 usage 字段，
	// 那种情况记成「0 单价」会把未知误判成免费，让这个号垄断该模型流量。
	// 这一条同时覆盖免费观测（credit=0 且 tokens>0 才是真的免费）。
	if usage.TotalTokens > 0 {
		NoteModelCost(acc.ID, meta.UpstreamModel, usage.Credit, usage.TotalTokens)
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
