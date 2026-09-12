package service

import (
	"encoding/json"
	"strings"

	"codebuddy-gateway/global"
)

// 预览长度是「留证据」和「别把库撑爆」之间的折中：
// 请求侧保留足够还原用户问了什么，响应侧保留足够还原模型答了什么。
const (
	maxRequestPreview  = 16 * 1024
	maxResponsePreview = 16 * 1024
	maxRawUsageLen     = 4 * 1024
)

// CaptureEnabled 由环境变量 GATEWAY_CAPTURE 控制（默认开启）。
// 关掉后只记 token / 缓存等统计字段，不落请求和响应正文。
func CaptureEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(global.CORE_CONFIG.Gateway.Capture))
	if v == "" {
		return true
	}
	switch v {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// captureChatRequest 从发往上游的 chat body 里抽出可读的对话内容。
// 上游 body 已经被本网关规整过，这里只是做展示层的再提取。
func captureChatRequest(raw []byte) string {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return clipText(string(raw), maxRequestPreview)
	}
	parts := make([]string, 0, 8)
	if msgs, ok := body["messages"].([]any); ok {
		for _, item := range msgs {
			msg, _ := item.(map[string]any)
			if msg == nil {
				continue
			}
			role, _ := msg["role"].(string)
			content := messageText(msg["content"])
			if content == "" {
				if calls := toolCallsText(msg["tool_calls"]); calls != "" {
					content = calls
				}
			}
			if content == "" {
				continue
			}
			if role == "" {
				role = "unknown"
			}
			parts = append(parts, "["+role+"] "+content)
		}
	}
	if len(parts) == 0 {
		return clipText(string(raw), maxRequestPreview)
	}
	return clipText(strings.Join(parts, "\n\n"), maxRequestPreview)
}

func messageText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		chunks := make([]string, 0, len(v))
		for _, item := range v {
			switch part := item.(type) {
			case string:
				chunks = append(chunks, part)
			case map[string]any:
				if text, ok := part["text"].(string); ok && text != "" {
					chunks = append(chunks, text)
					continue
				}
				if t, ok := part["type"].(string); ok {
					chunks = append(chunks, "["+t+"]")
				}
			}
		}
		return strings.Join(chunks, "\n")
	default:
		return ""
	}
}

func toolCallsText(raw any) string {
	calls, ok := raw.([]any)
	if !ok || len(calls) == 0 {
		return ""
	}
	out := make([]string, 0, len(calls))
	for _, item := range calls {
		call, _ := item.(map[string]any)
		if call == nil {
			continue
		}
		fn, _ := call["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if name == "" {
			name, _ = call["name"].(string)
		}
		args, _ := fn["arguments"].(string)
		if args == "" {
			if rawArgs, ok := call["arguments"]; ok {
				if encoded, err := json.Marshal(rawArgs); err == nil {
					args = string(encoded)
				}
			}
		}
		out = append(out, "tool_call "+name+"("+clipText(args, 400)+")")
	}
	return strings.Join(out, "\n")
}

// CaptureCollector 累积一次请求的响应文本，供 dashboard 回看。
type CaptureCollector struct {
	buf       strings.Builder
	truncated bool
	limit     int
}

func NewCaptureCollector() *CaptureCollector {
	if !CaptureEnabled() {
		return nil
	}
	return &CaptureCollector{limit: maxResponsePreview}
}

func (c *CaptureCollector) Write(s string) {
	if c == nil || s == "" || c.truncated {
		return
	}
	if c.buf.Len()+len(s) > c.limit {
		remain := c.limit - c.buf.Len()
		if remain > 0 {
			c.buf.WriteString(s[:remain])
		}
		c.truncated = true
		return
	}
	c.buf.WriteString(s)
}

// Written 返回实际落盘的字节数，未截断时等于响应全文长度。
func (c *CaptureCollector) Written() int {
	if c == nil {
		return 0
	}
	return c.buf.Len()
}

func (c *CaptureCollector) String() string {
	if c == nil {
		return ""
	}
	out := c.buf.String()
	if c.truncated {
		out += "\n…(已截断)"
	}
	return out
}

// clipRawUsage 保留上游 usage 原文（去掉体积大的细节字段），便于核对计费口径。
func clipRawUsage(raw map[string]any) string {
	if !CaptureEnabled() || raw == nil {
		return ""
	}
	slim := make(map[string]any, len(raw))
	for k, v := range raw {
		switch v.(type) {
		case string, float64, int, int64, bool, nil, json.Number:
			slim[k] = v
		}
	}
	if len(slim) == 0 {
		return ""
	}
	encoded, err := json.Marshal(slim)
	if err != nil {
		return ""
	}
	return clipText(string(encoded), maxRawUsageLen)
}

func clipText(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "…(已截断)"
}

// chunkText 从上游 SSE 行里抽出可读文本，用于 dashboard 回看响应。
func chunkText(line string) string {
	if line == "" {
		return ""
	}
	if !strings.HasPrefix(line, "data: ") {
		return ""
	}
	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" {
		return ""
	}
	var chunk map[string]any
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return ""
	}
	var out strings.Builder
	if choices, ok := chunk["choices"].([]any); ok {
		for _, item := range choices {
			choice, _ := item.(map[string]any)
			if choice == nil {
				continue
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
			if text, ok := delta["reasoning_content"].(string); ok && text != "" {
				out.WriteString("[think] " + text + "\n")
			}
			if text, ok := delta["content"].(string); ok && text != "" {
				out.WriteString(text)
			}
			if calls, ok := delta["tool_calls"].([]any); ok {
				for _, raw := range calls {
					call, _ := raw.(map[string]any)
					if call == nil {
						continue
					}
					fn, _ := call["function"].(map[string]any)
					name, _ := fn["name"].(string)
					args, _ := fn["arguments"].(string)
					if name == "" && args == "" {
						continue
					}
					out.WriteString("\n[tool_call " + name + " " + clipText(args, 400) + "]\n")
				}
			}
		}
	}
	return out.String()
}

// jsonResponseText 把聚合后的非流式响应拉平成可读文本。
func jsonResponseText(result *ChatResult) string {
	if result == nil {
		return ""
	}
	var out strings.Builder
	if result.Reasoning != "" {
		out.WriteString("[think] " + result.Reasoning + "\n")
	}
	if result.Content != "" {
		out.WriteString(result.Content)
	}
	for _, call := range result.ToolCalls {
		out.WriteString("\n[tool_call " + call.Name + " " + clipText(call.Arguments, 400) + "]\n")
	}
	return out.String()
}
