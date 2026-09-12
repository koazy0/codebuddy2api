package service

import "strings"

const (
	codexFingerprintPrefix = "You are a coding agent running in the Codex CLI, a terminal-based coding assistant. Codex CLI is an open source project led by OpenAI. You are expected to be precise, safe, and helpful."
	sanitizedAgentPrefix   = "You are a coding agent running in the Codex CLI, a terminal-based coding assistant. You are expected to be precise, safe, and helpful."
)

func sanitizeUpstreamChat(body map[string]any) {
	if body == nil {
		return
	}
	msgs, ok := body["messages"].([]any)
	if !ok {
		return
	}
	for _, item := range msgs {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(asString(m["role"])))
		if role != "system" && role != "developer" {
			continue
		}
		m["content"] = sanitizeContentValue(m["content"])
	}
}

func sanitizeContentValue(v any) any {
	switch t := v.(type) {
	case string:
		return sanitizeSystemText(t)
	case []any:
		for i, part := range t {
			t[i] = sanitizeContentValue(part)
		}
		return t
	case map[string]any:
		if text, ok := t["text"].(string); ok {
			t["text"] = sanitizeSystemText(text)
		}
		if content, ok := t["content"]; ok {
			t["content"] = sanitizeContentValue(content)
		}
		return t
	default:
		return v
	}
}

func sanitizeSystemText(s string) string {
	if s == "" {
		return s
	}
	return strings.ReplaceAll(s, codexFingerprintPrefix, sanitizedAgentPrefix)
}

func isUnapprovedChannel(raw []byte) bool {
	s := string(raw)
	if strings.Contains(s, "11128") {
		return true
	}
	return strings.Contains(strings.ToLower(s), "unapproved channel")
}

func isUpstreamModelUnavailable(raw []byte) bool {
	s := string(raw)
	if strings.Contains(s, "11102") {
		return true
	}
	low := strings.ToLower(s)
	return strings.Contains(low, "only available for authorized users") ||
		strings.Contains(low, "the requested model is not available")
}

func isUpstreamRequestError(raw []byte) bool {
	return isUnapprovedChannel(raw) || isUpstreamModelUnavailable(raw)
}
