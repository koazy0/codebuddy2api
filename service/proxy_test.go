package service

import (
	"strings"
	"testing"

	"codebuddy-gateway/config"
	"codebuddy-gateway/global"
)

func TestPrepareChatBody(t *testing.T) {
	global.CORE_CONFIG.Gateway = config.Gateway{
		Passthrough:     true,
		InjectReasoning: false,
		ModelAlias:      []config.ModelAlias{{From: "gpt-4", To: "glm-5.1"}},
	}
	meta, err := PrepareChatBody([]byte(`{"model":"gpt-4","stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if meta.RequestedModel != "gpt-4" {
		t.Fatalf("requested=%s", meta.RequestedModel)
	}
	if meta.UpstreamModel != "glm-5.1" {
		t.Fatalf("upstream=%s", meta.UpstreamModel)
	}
	if meta.ClientStream {
		t.Fatal("client did not want stream")
	}
	if !strings.Contains(string(meta.Body), `"stream":true`) {
		t.Fatalf("body should force stream: %s", meta.Body)
	}
}

func TestRewriteSSELinePassthrough(t *testing.T) {
	global.CORE_CONFIG.Gateway.Passthrough = true
	line := `data: {"id":"1","model":"ep-xxx","choices":[{"delta":{"content":"hi","reasoning_content":"think"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"credit":0.16}}`
	out, usage := rewriteSSELine(line, "glm-5.1")
	if !strings.Contains(out, `"model":"glm-5.1"`) {
		t.Fatalf("model not rewritten: %s", out)
	}
	if !strings.Contains(out, "reasoning_content") {
		t.Fatalf("passthrough should keep reasoning: %s", out)
	}
	if usage == nil || usage.Credit != 0.16 || usage.PromptTokens != 1 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestRewriteSSELineStrip(t *testing.T) {
	global.CORE_CONFIG.Gateway.Passthrough = false
	line := `data: {"model":"ep-xxx","choices":[{"delta":{"content":"hi","reasoning_content":"think"}}],"usage":{"credit":1}}`
	out, _ := rewriteSSELine(line, "glm-5.1")
	if strings.Contains(out, "reasoning_content") {
		t.Fatalf("should strip reasoning: %s", out)
	}
	if strings.Contains(out, `"credit"`) {
		t.Fatalf("should strip credit: %s", out)
	}
}

func TestAggregateSSE(t *testing.T) {
	global.CORE_CONFIG.Gateway.Passthrough = true
	raw := strings.Join([]string{
		`data: {"id":"abc","model":"ep-1","choices":[{"delta":{"content":"Hel","reasoning_content":"t"}}]}`,
		`data: {"choices":[{"delta":{"content":"lo"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"credit":0.2}}`,
		`data: [DONE]`,
	}, "\n")
	body, usage, err := aggregateSSE(strings.NewReader(raw), "glm-5.1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, `"content":"Hello"`) {
		t.Fatalf("content missing: %s", s)
	}
	if !strings.Contains(s, `"model":"glm-5.1"`) {
		t.Fatalf("model missing: %s", s)
	}
	if usage == nil || usage.TotalTokens != 5 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestRewriteSSELineKeepsCacheFields(t *testing.T) {
	global.CORE_CONFIG.Gateway.Passthrough = false
	line := `data: {"model":"ep-xxx","choices":[{"delta":{"content":"hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"credit":1.2,"prompt_cache_hit_tokens":8,"prompt_cache_miss_tokens":2,"prompt_tokens_details":{"cached_tokens":8}}}`
	out, usage := rewriteSSELine(line, "glm-5.3")
	if strings.Contains(out, `"credit"`) {
		t.Fatalf("credit should still be stripped: %s", out)
	}
	if !strings.Contains(out, `"prompt_cache_hit_tokens":8`) {
		t.Fatalf("cache hit missing: %s", out)
	}
	if !strings.Contains(out, `"cached_tokens":8`) {
		t.Fatalf("prompt_tokens_details missing: %s", out)
	}
	if usage == nil || usage.CacheHitTokens != 8 || usage.CacheMissTokens != 2 {
		t.Fatalf("usage=%+v", usage)
	}
}
