package service

import (
	"encoding/json"
	"strings"
)

type parsedUsage struct {
	PromptTokens             int
	CompletionTokens         int
	TotalTokens              int
	ThinkingTokens           int
	Credit                   float64
	CacheHitTokens           int
	CacheMissTokens          int
	CacheHitRate             float64
	CacheReadInputTokens     int
	CacheCreationInputTokens int
	CacheWriteTokens         int
	CachedTokens             int
	FirstTokenMs             int64
	LatencyMs                int64
	TokensPerSecond          float64
	OutputTokensPerSecond    float64
	RequestID                string
}

func extractUsage(chunk map[string]any) *parsedUsage {
	if chunk == nil {
		return nil
	}
	u := &parsedUsage{}
	if id, ok := chunk["id"].(string); ok {
		u.RequestID = id
	}
	raw, ok := chunk["usage"].(map[string]any)
	if !ok || raw == nil {
		if u.RequestID == "" {
			return nil
		}
		return u
	}
	u.PromptTokens = asInt(raw["prompt_tokens"])
	u.CompletionTokens = asInt(raw["completion_tokens"])
	u.TotalTokens = asInt(raw["total_tokens"])
	u.Credit = asFloat(raw["credit"])
	u.CacheHitTokens = asInt(raw["prompt_cache_hit_tokens"])
	u.CacheMissTokens = asInt(raw["prompt_cache_miss_tokens"])
	u.CacheReadInputTokens = asInt(raw["cache_read_input_tokens"])
	u.CacheCreationInputTokens = asInt(raw["cache_creation_input_tokens"])
	u.CacheWriteTokens = asInt(raw["prompt_cache_write_tokens"])
	if u.CacheWriteTokens == 0 {
		u.CacheWriteTokens = asInt(raw["cache_write_tokens"])
	}
	u.CachedTokens = asInt(raw["cached_tokens"])
	u.ThinkingTokens = asInt(raw["completion_thinking_tokens"])
	if details, ok := raw["completion_tokens_details"].(map[string]any); ok {
		if v := asInt(details["reasoning_tokens"]); v > 0 {
			u.ThinkingTokens = v
		}
		if v := asInt(details["cached_tokens"]); v > 0 && u.CachedTokens == 0 {
			u.CachedTokens = v
		}
	}
	if details, ok := raw["prompt_tokens_details"].(map[string]any); ok {
		if v := asInt(details["cached_tokens"]); v > 0 && u.CacheHitTokens == 0 {
			u.CacheHitTokens = v
		}
	}
	if u.TotalTokens == 0 {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	return u
}

func mergeUsage(dst, src *parsedUsage) *parsedUsage {
	if src == nil {
		return dst
	}
	if dst == nil {
		cp := *src
		return &cp
	}
	if src.RequestID != "" {
		dst.RequestID = src.RequestID
	}
	if src.PromptTokens > 0 || src.CompletionTokens > 0 || src.Credit > 0 || src.ThinkingTokens > 0 {
		dst.PromptTokens = src.PromptTokens
		dst.CompletionTokens = src.CompletionTokens
		dst.TotalTokens = src.TotalTokens
		dst.ThinkingTokens = src.ThinkingTokens
		dst.Credit = src.Credit
		dst.CacheHitTokens = src.CacheHitTokens
		dst.CacheMissTokens = src.CacheMissTokens
		dst.CacheReadInputTokens = src.CacheReadInputTokens
		dst.CacheCreationInputTokens = src.CacheCreationInputTokens
		dst.CacheWriteTokens = src.CacheWriteTokens
		dst.CachedTokens = src.CachedTokens
	}
	return dst
}

func deriveMetrics(u *parsedUsage, latencyMs int64) {
	if u == nil {
		return
	}
	u.LatencyMs = latencyMs
	den := u.CacheHitTokens + u.CacheMissTokens
	if den > 0 {
		u.CacheHitRate = float64(u.CacheHitTokens) / float64(den)
	} else if u.PromptTokens > 0 && u.CacheHitTokens > 0 {
		u.CacheHitRate = float64(u.CacheHitTokens) / float64(u.PromptTokens)
	}
	if latencyMs > 0 && u.CompletionTokens > 0 {
		u.TokensPerSecond = float64(u.CompletionTokens) / (float64(latencyMs) / 1000.0)
	}
	gen := latencyMs - u.FirstTokenMs
	if gen > 0 && u.CompletionTokens > 0 {
		u.OutputTokensPerSecond = float64(u.CompletionTokens) / (float64(gen) / 1000.0)
	}
}

func lineHasGeneratedToken(line string) bool {
	if !strings.HasPrefix(line, "data: ") {
		return false
	}
	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" {
		return false
	}
	var chunk map[string]any
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return false
	}
	choices, _ := chunk["choices"].([]any)
	for _, item := range choices {
		choice, _ := item.(map[string]any)
		if choice == nil {
			continue
		}
		delta, _ := choice["delta"].(map[string]any)
		if delta == nil {
			continue
		}
		if s, ok := delta["content"].(string); ok && s != "" {
			return true
		}
		if s, ok := delta["reasoning_content"].(string); ok && s != "" {
			return true
		}
		if tcs, ok := delta["tool_calls"].([]any); ok && len(tcs) > 0 {
			return true
		}
	}
	return false
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}
