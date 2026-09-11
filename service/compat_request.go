package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"codebuddy-gateway/global"
)

func PrepareResponsesBody(raw []byte) (*ChatRequestMeta, error) {
	chat, err := responsesToChat(raw)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(chat)
	if err != nil {
		return nil, err
	}
	meta, err := PrepareChatBody(encoded)
	if err != nil {
		return nil, err
	}
	meta.Protocol = ProtocolResponses
	return meta, nil
}

func PrepareAnthropicBody(raw []byte) (*ChatRequestMeta, error) {
	chat, err := anthropicToChat(raw)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(chat)
	if err != nil {
		return nil, err
	}
	meta, err := PrepareChatBody(encoded)
	if err != nil {
		return nil, err
	}
	meta.Protocol = ProtocolAnthropic
	return meta, nil
}

func responsesToChat(raw []byte) (map[string]any, error) {
	var src map[string]any
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, fmt.Errorf("invalid json body")
	}
	model, _ := src["model"].(string)
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	chat := map[string]any{"model": model}
	copyChatFields(chat, src, "stream", "temperature", "top_p", "user", "n", "stop", "metadata")
	if v, ok := src["max_output_tokens"]; ok {
		chat["max_tokens"] = v
	}
	if v, ok := src["parallel_tool_calls"]; ok {
		chat["parallel_tool_calls"] = v
	}

	messages := make([]any, 0, 4)
	if inst := extractTextContent(src["instructions"]); inst != "" {
		messages = append(messages, map[string]any{"role": "system", "content": inst})
	}
	messages = append(messages, convertResponsesInput(src["input"])...)
	chat["messages"] = messages

	if tools := convertResponsesTools(src["tools"]); tools != nil {
		chat["tools"] = tools
	}
	if tc := convertResponsesToolChoice(src["tool_choice"]); tc != nil {
		chat["tool_choice"] = tc
	}
	if r, ok := src["reasoning"].(map[string]any); ok {
		if effort := asString(r["effort"]); effort != "" {
			chat["reasoningEffort"] = effort
		}
		if summary := asString(r["summary"]); summary != "" {
			chat["reasoning_summary"] = summary
		}
	}
	return chat, nil
}

func convertResponsesInput(v any) []any {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		if s == "" {
			return nil
		}
		return []any{map[string]any{"role": "user", "content": s}}
	}
	arr, ok := v.([]any)
	if !ok {
		if m, ok := v.(map[string]any); ok {
			arr = []any{m}
		} else {
			text := extractTextContent(v)
			if text == "" {
				return nil
			}
			return []any{map[string]any{"role": "user", "content": text}}
		}
	}
	messages := make([]any, 0, len(arr))
	var pending []any
	flush := func() {
		if len(pending) == 0 {
			return
		}
		messages = append(messages, map[string]any{
			"role":       "assistant",
			"content":    nil,
			"tool_calls": pending,
		})
		pending = nil
	}
	for _, item := range arr {
		if s, ok := item.(string); ok {
			flush()
			messages = append(messages, map[string]any{"role": "user", "content": s})
			continue
		}
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		typ := asString(m["type"])
		role := asString(m["role"])
		switch typ {
		case "function_call":
			pending = append(pending, responsesFunctionCallToToolCall(m))
		case "function_call_output", "tool_result":
			flush()
			callID := asString(m["call_id"])
			if callID == "" {
				callID = asString(m["tool_use_id"])
			}
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": callID,
				"content":      extractTextContent(firstNonNil(m["output"], m["content"])),
			})
		case "reasoning", "item_reference":
			continue
		default:
			flush()
			if role == "" {
				role = "user"
			}
			content := convertResponsesContent(m["content"])
			if content == nil || content == "" {
				if t := asString(m["text"]); t != "" {
					content = t
				}
			}
			messages = append(messages, map[string]any{"role": role, "content": content})
		}
	}
	flush()
	return messages
}

func responsesFunctionCallToToolCall(m map[string]any) map[string]any {
	id := asString(m["call_id"])
	if id == "" {
		id = asString(m["id"])
	}
	name := asString(m["name"])
	args := asString(m["arguments"])
	if args == "" {
		args = mustJSON(m["input"])
	}
	return map[string]any{
		"id":   id,
		"type": "function",
		"function": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
}

func convertResponsesContent(v any) any {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	arr, ok := v.([]any)
	if !ok {
		return extractTextContent(v)
	}
	parts := make([]any, 0, len(arr))
	var text strings.Builder
	onlyText := true
	for _, item := range arr {
		if s, ok := item.(string); ok {
			text.WriteString(s)
			parts = append(parts, map[string]any{"type": "text", "text": s})
			continue
		}
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		typ := asString(m["type"])
		switch typ {
		case "input_text", "output_text", "text", "summary_text":
			t := asString(m["text"])
			text.WriteString(t)
			parts = append(parts, map[string]any{"type": "text", "text": t})
		case "input_image", "image_url", "image":
			onlyText = false
			parts = append(parts, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": imageURLFromPart(m)},
			})
		default:
			if t := asString(m["text"]); t != "" {
				text.WriteString(t)
				parts = append(parts, map[string]any{"type": "text", "text": t})
			}
		}
	}
	if onlyText {
		return text.String()
	}
	return parts
}

func convertResponsesTools(v any) any {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]any, 0, len(arr))
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		if _, ok := m["function"]; ok {
			out = append(out, m)
			continue
		}
		typ := asString(m["type"])
		if typ != "" && typ != "function" {
			continue
		}
		name := asString(m["name"])
		if name == "" {
			continue
		}
		fn := map[string]any{"name": name}
		if d, ok := m["description"]; ok {
			fn["description"] = d
		}
		if p, ok := m["parameters"]; ok {
			fn["parameters"] = p
		} else if p, ok := m["input_schema"]; ok {
			fn["parameters"] = p
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func convertResponsesToolChoice(v any) any {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return s
	}
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	if _, ok := m["function"]; ok {
		return m
	}
	typ := asString(m["type"])
	switch typ {
	case "auto", "none", "required":
		return typ
	case "function":
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": asString(m["name"])},
		}
	default:
		return v
	}
}

func anthropicToChat(raw []byte) (map[string]any, error) {
	var src map[string]any
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, fmt.Errorf("invalid json body")
	}
	model, _ := src["model"].(string)
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	chat := map[string]any{"model": model}
	copyChatFields(chat, src, "stream", "temperature", "top_p", "user", "metadata")
	if v, ok := src["max_tokens"]; ok {
		chat["max_tokens"] = v
	} else {
		chat["max_tokens"] = defaultMaxTokens()
	}
	if v, ok := src["stop_sequences"]; ok {
		chat["stop"] = v
	}

	messages := make([]any, 0, 4)
	if sys := extractAnthropicSystem(src["system"]); sys != "" {
		messages = append(messages, map[string]any{"role": "system", "content": sys})
	}
	messages = append(messages, convertAnthropicMessages(src["messages"])...)
	chat["messages"] = messages

	if tools := convertAnthropicTools(src["tools"]); tools != nil {
		chat["tools"] = tools
	}
	if tc := convertAnthropicToolChoice(src["tool_choice"]); tc != nil {
		chat["tool_choice"] = tc
	}
	if th, ok := src["thinking"].(map[string]any); ok {
		typ := asString(th["type"])
		if typ == "enabled" || typ == "adaptive" {
			chat["reasoningEffort"] = "medium"
			chat["reasoning_summary"] = "auto"
		}
	}
	return chat, nil
}

func extractAnthropicSystem(v any) string {
	return extractTextContent(v)
}

func convertAnthropicMessages(v any) []any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(arr))
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		role := asString(m["role"])
		if role == "" {
			role = "user"
		}
		if s, ok := m["content"].(string); ok {
			out = append(out, map[string]any{"role": role, "content": s})
			continue
		}
		parts, _ := m["content"].([]any)
		if len(parts) == 0 {
			out = append(out, map[string]any{"role": role, "content": extractTextContent(m["content"])})
			continue
		}
		var textParts []any
		var toolCalls []any
		onlyText := true
		for _, part := range parts {
			pm, _ := part.(map[string]any)
			if pm == nil {
				if s, ok := part.(string); ok && s != "" {
					textParts = append(textParts, map[string]any{"type": "text", "text": s})
				}
				continue
			}
			switch asString(pm["type"]) {
			case "text":
				textParts = append(textParts, map[string]any{"type": "text", "text": asString(pm["text"])})
			case "image":
				onlyText = false
				textParts = append(textParts, map[string]any{
					"type":      "image_url",
					"image_url": map[string]any{"url": imageURLFromPart(pm)},
				})
			case "tool_use":
				toolCalls = append(toolCalls, map[string]any{
					"id":   asString(pm["id"]),
					"type": "function",
					"function": map[string]any{
						"name":      asString(pm["name"]),
						"arguments": mustJSON(pm["input"]),
					},
				})
			case "tool_result":
				out = append(out, map[string]any{
					"role":         "tool",
					"tool_call_id": asString(firstNonNil(pm["tool_use_id"], pm["id"])),
					"content":      extractTextContent(pm["content"]),
				})
			}
		}
		if len(toolCalls) > 0 {
			msg := map[string]any{
				"role":       "assistant",
				"tool_calls": toolCalls,
			}
			if onlyText {
				msg["content"] = joinTextParts(textParts)
			} else if len(textParts) > 0 {
				msg["content"] = textParts
			} else {
				msg["content"] = nil
			}
			out = append(out, msg)
			continue
		}
		if len(textParts) == 0 {
			continue
		}
		if role == "tool" {
			out = append(out, map[string]any{
				"role":         "tool",
				"tool_call_id": asString(m["tool_call_id"]),
				"content":      joinTextParts(textParts),
			})
			continue
		}
		msg := map[string]any{"role": role}
		if onlyText {
			msg["content"] = joinTextParts(textParts)
		} else {
			msg["content"] = textParts
		}
		out = append(out, msg)
	}
	return out
}

func convertAnthropicTools(v any) any {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]any, 0, len(arr))
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		if _, ok := m["function"]; ok {
			out = append(out, m)
			continue
		}
		name := asString(m["name"])
		if name == "" {
			continue
		}
		fn := map[string]any{"name": name}
		if d, ok := m["description"]; ok {
			fn["description"] = d
		}
		if p, ok := m["input_schema"]; ok {
			fn["parameters"] = p
		} else if p, ok := m["parameters"]; ok {
			fn["parameters"] = p
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func convertAnthropicToolChoice(v any) any {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return s
	}
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	switch asString(m["type"]) {
	case "auto":
		return "auto"
	case "none":
		return "none"
	case "any":
		return "required"
	case "tool":
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": asString(m["name"])},
		}
	default:
		return v
	}
}

func imageURLFromPart(m map[string]any) string {
	if s := asString(m["image_url"]); s != "" {
		return s
	}
	if nested, ok := m["image_url"].(map[string]any); ok {
		if s := asString(nested["url"]); s != "" {
			return s
		}
	}
	if src, ok := m["source"].(map[string]any); ok {
		if asString(src["type"]) == "url" {
			if s := asString(src["url"]); s != "" {
				return s
			}
		}
		media := asString(src["media_type"])
		data := asString(src["data"])
		if media != "" && data != "" {
			return "data:" + media + ";base64," + data
		}
		if s := asString(src["url"]); s != "" {
			return s
		}
	}
	if s := asString(m["url"]); s != "" {
		return s
	}
	return extractTextContent(m)
}

func joinTextParts(parts []any) string {
	var b strings.Builder
	for _, part := range parts {
		m, _ := part.(map[string]any)
		if m == nil {
			continue
		}
		b.WriteString(asString(m["text"]))
	}
	return b.String()
}

func mustJSON(v any) string {
	if v == nil {
		return "{}"
	}
	if s, ok := v.(string); ok {
		if s == "" {
			return "{}"
		}
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func defaultMaxTokens() int {
	n := global.CORE_CONFIG.CodeBuddy.DefaultMaxTokens
	if n <= 0 {
		return 32000
	}
	return n
}
