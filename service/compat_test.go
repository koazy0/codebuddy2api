package service

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"codebuddy-gateway/config"
	"codebuddy-gateway/global"
)

func TestPrepareResponsesBody(t *testing.T) {
	global.CORE_CONFIG.Gateway = config.Gateway{Passthrough: true}
	meta, err := PrepareResponsesBody([]byte(`{
		"model":"glm-5.2",
		"stream":false,
		"instructions":"you are helpful",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{\"cmd\":\"ls\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"ok"}
		],
		"tools":[{"type":"function","name":"shell","description":"run","parameters":{"type":"object"}}],
		"max_output_tokens":128
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Protocol != ProtocolResponses {
		t.Fatalf("protocol=%s", meta.Protocol)
	}
	if meta.ClientStream {
		t.Fatal("client did not want stream")
	}
	var body map[string]any
	if err := json.Unmarshal(meta.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["max_tokens"] != float64(128) {
		t.Fatalf("max_tokens=%v", body["max_tokens"])
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) < 3 {
		t.Fatalf("messages=%v", msgs)
	}
	sys, _ := msgs[0].(map[string]any)
	if sys["role"] != "system" || sys["content"] != "you are helpful" {
		t.Fatalf("system=%v", sys)
	}
	user, _ := msgs[1].(map[string]any)
	if user["role"] != "user" || user["content"] != "hi" {
		t.Fatalf("user=%v", user)
	}
	asst, _ := msgs[2].(map[string]any)
	if asst["role"] != "assistant" {
		t.Fatalf("assistant=%v", asst)
	}
	tool, _ := msgs[3].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" {
		t.Fatalf("tool=%v", tool)
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools=%v", tools)
	}
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "shell" {
		t.Fatalf("tool fn=%v", fn)
	}
}

func TestPrepareAnthropicBody(t *testing.T) {
	global.CORE_CONFIG.Gateway = config.Gateway{Passthrough: true}
	meta, err := PrepareAnthropicBody([]byte(`{
		"model":"glm-5.2",
		"max_tokens":64,
		"stream":true,
		"system":[{"type":"text","text":"sys"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hi"}]},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"cmd":"ls"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}
		],
		"tools":[{"name":"Bash","description":"run","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"auto"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Protocol != ProtocolAnthropic {
		t.Fatalf("protocol=%s", meta.Protocol)
	}
	var body map[string]any
	if err := json.Unmarshal(meta.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["tool_choice"] != "auto" {
		t.Fatalf("tool_choice=%v", body["tool_choice"])
	}
	msgs, _ := body["messages"].([]any)
	roles := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		roles = append(roles, msg.(map[string]any)["role"].(string))
	}
	joined := strings.Join(roles, ",")
	if joined != "system,user,assistant,tool" {
		t.Fatalf("roles=%s msgs=%v", joined, msgs)
	}
	asst := msgs[2].(map[string]any)
	calls, _ := asst["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("tool_calls=%v", asst)
	}
}

func TestEncodeResponsesAndAnthropicJSON(t *testing.T) {
	global.CORE_CONFIG.Gateway.Passthrough = true
	result := &ChatResult{
		ID:           "abc",
		Model:        "glm-5.2",
		Created:      1,
		Content:      "hello",
		Reasoning:    "think",
		FinishReason: "stop",
		Usage:        &parsedUsage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	}
	raw, err := encodeResponsesJSON(result)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"object":"response"`) || !strings.Contains(s, `"type":"output_text"`) || !strings.Contains(s, `"hello"`) {
		t.Fatalf("responses json=%s", s)
	}
	if !strings.Contains(s, `"input_tokens":3`) {
		t.Fatalf("usage missing: %s", s)
	}
	raw, err = encodeAnthropicJSON(result)
	if err != nil {
		t.Fatal(err)
	}
	s = string(raw)
	if !strings.Contains(s, `"type":"message"`) || !strings.Contains(s, `"stop_reason":"end_turn"`) || !strings.Contains(s, `"thinking"`) {
		t.Fatalf("anthropic json=%s", s)
	}
}

func TestResponsesStreamAdapter(t *testing.T) {
	global.CORE_CONFIG.Gateway.Passthrough = true
	var buf bytes.Buffer
	ad := newStreamAdapter(ProtocolResponses, &buf, nil, "glm-5.2")
	ad.start()
	ad.onText("Hel")
	ad.onText("lo")
	ad.onFinishReason("stop")
	ad.setUsage(&parsedUsage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3})
	if err := ad.finish(); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, ev := range []string{
		"event: response.created",
		"event: response.output_item.added",
		"event: response.content_part.added",
		"event: response.output_text.delta",
		"event: response.completed",
	} {
		if !strings.Contains(s, ev) {
			t.Fatalf("missing %s in %s", ev, s)
		}
	}
	if !strings.Contains(s, `"delta":"Hel"`) || !strings.Contains(s, `"delta":"lo"`) {
		t.Fatalf("deltas missing: %s", s)
	}
}

func TestAnthropicStreamAdapter(t *testing.T) {
	global.CORE_CONFIG.Gateway.Passthrough = true
	var buf bytes.Buffer
	ad := newStreamAdapter(ProtocolAnthropic, &buf, nil, "glm-5.2")
	ad.start()
	ad.onReasoning("why")
	ad.onText("ok")
	ad.onFinishReason("stop")
	ad.setUsage(&parsedUsage{CompletionTokens: 2})
	if err := ad.finish(); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, ev := range []string{
		"event: message_start",
		"event: content_block_start",
		"event: content_block_delta",
		"event: content_block_stop",
		"event: message_delta",
		"event: message_stop",
		`"type":"thinking_delta"`,
		`"type":"text_delta"`,
	} {
		if !strings.Contains(s, ev) {
			t.Fatalf("missing %s in %s", ev, s)
		}
	}
}

func TestCollectSSEToolCalls(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"id":"x","model":"ep","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Bash","arguments":""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":1}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n")
	result, err := collectSSE(strings.NewReader(raw), "glm-5.2")
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "glm-5.2" {
		t.Fatalf("model=%s", result.Model)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "Bash" || result.ToolCalls[0].Arguments != `{"a":1}` {
		t.Fatalf("tools=%+v", result.ToolCalls)
	}
	if result.FinishReason != "tool_calls" {
		t.Fatalf("finish=%s", result.FinishReason)
	}
}

func TestDeveloperRoleMapsToSystem(t *testing.T) {
	global.CORE_CONFIG.Gateway = config.Gateway{Passthrough: true}
	meta, err := PrepareResponsesBody([]byte(`{
		"model":"glm-5.2",
		"instructions":"base",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"dev rules"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(meta.Body, &body); err != nil {
		t.Fatal(err)
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages=%v", msgs)
	}
	dev, _ := msgs[1].(map[string]any)
	if dev["role"] != "system" || dev["content"] != "dev rules" {
		t.Fatalf("developer mapped=%v", dev)
	}
}

func TestSanitizeCodexFingerprint(t *testing.T) {
	global.CORE_CONFIG.Gateway = config.Gateway{Passthrough: true}
	prefix := "You are a coding agent running in the Codex CLI, a terminal-based coding assistant. Codex CLI is an open source project led by OpenAI. You are expected to be precise, safe, and helpful."
	payload := map[string]any{
		"model":        "deepseek-v4.1-flash",
		"instructions": prefix + " Keep secrets.",
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "developer",
				"content": []any{
					map[string]any{"type": "input_text", "text": "Use Codex CLI with OpenAI."},
				},
			},
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "Codex CLI please"},
				},
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := PrepareResponsesBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(meta.Body, &body); err != nil {
		t.Fatal(err)
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages=%v", msgs)
	}
	sys := msgs[0].(map[string]any)["content"].(string)
	if strings.Contains(sys, "open source project led by OpenAI") {
		t.Fatalf("system still has WAF fingerprint: %s", sys[:200])
	}
	if !strings.Contains(sys, "Codex CLI") || !strings.Contains(sys, "Keep secrets.") {
		t.Fatalf("native Codex identity or remainder lost: %s", sys)
	}
	dev := msgs[1].(map[string]any)["content"].(string)
	if dev != "Use Codex CLI with OpenAI." {
		t.Fatalf("developer should stay intact: %s", dev)
	}
	user := msgs[2].(map[string]any)["content"].(string)
	if user != "Codex CLI please" {
		t.Fatalf("user content should stay intact: %s", user)
	}
}

func TestIsUnapprovedChannel(t *testing.T) {
	if !isUnapprovedChannel([]byte(`{"code":11128,"msg":"Illegal API invocation from an unapproved channel"}`)) {
		t.Fatal("expected 11128 to match")
	}
	if isUnapprovedChannel([]byte(`{"code":0,"msg":"ok"}`)) {
		t.Fatal("false positive")
	}
	if !isUpstreamRequestError([]byte(`{"code":11102,"msg":"model [gpt-5.6-luna] is only available for authorized users"}`)) {
		t.Fatal("expected 11102 to be a request error")
	}
	if isUpstreamRequestError([]byte(`{"code":0,"msg":"ok"}`)) {
		t.Fatal("ok should not be a request error")
	}
}

func TestConvertNamespaceAndCustomTools(t *testing.T) {
	global.CORE_CONFIG.Gateway = config.Gateway{Passthrough: true}
	meta, err := PrepareResponsesBody([]byte(`{
		"model":"deepseek-v4.1-flash",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"tools":[
			{"type":"function","name":"wait","parameters":{"type":"object"}},
			{"type":"custom","name":"exec","description":"run command","format":{"type":"grammar"}},
			{"type":"namespace","name":"multi_agent_v1","tools":[
				{"type":"function","name":"spawn_agent","description":"spawn","parameters":{"type":"object"}}
			]},
			{"type":"web_search"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(meta.Body, &body); err != nil {
		t.Fatal(err)
	}
	tools, _ := body["tools"].([]any)
	names := make([]string, 0, len(tools))
	for _, item := range tools {
		fn := item.(map[string]any)["function"].(map[string]any)
		names = append(names, fn["name"].(string))
	}
	joined := strings.Join(names, ",")
	if joined != "wait,exec,multi_agent_v1__spawn_agent" {
		t.Fatalf("tools=%s full=%v", joined, tools)
	}
}

func TestUnwrapFreeformArgs(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: a.txt\n+hi\n*** End Patch"
	wrapped, err := json.Marshal(map[string]string{"input": patch})
	if err != nil {
		t.Fatal(err)
	}
	if got := unwrapFreeformArgs(string(wrapped)); got != patch {
		t.Fatalf("unwrap json: %q", got)
	}
	if got := unwrapFreeformArgs(patch); got != patch {
		t.Fatalf("unwrap raw: %q", got)
	}
	if got := unwrapFreeformArgs(`{"cmd":"ls"}`); got != `{"cmd":"ls"}` {
		t.Fatalf("non-input json should stay: %q", got)
	}
	if !isFreeformTool("apply_patch") || !isFreeformTool("Apply_Patch") || !isFreeformTool("ns__apply_patch") {
		t.Fatal("apply_patch should be freeform")
	}
	if isFreeformTool("exec_command") || isFreeformTool("shell") {
		t.Fatal("json tools must stay functions")
	}
	got := ensureFreeformJSONArgs(patch)
	var obj map[string]string
	if err := json.Unmarshal([]byte(got), &obj); err != nil || obj["input"] != patch {
		t.Fatalf("wrap raw: %s", got)
	}
	if ensureFreeformJSONArgs(string(wrapped)) != string(wrapped) {
		t.Fatalf("already-json should stay: %s", ensureFreeformJSONArgs(string(wrapped)))
	}
}

func TestApplyPatchCustomToolRoundTrip(t *testing.T) {
	global.CORE_CONFIG.Gateway = config.Gateway{Passthrough: true}
	patch := "*** Begin Patch\n*** Update File: web/app.js\n@@\n+const x = 1;\n*** End Patch"
	payload := map[string]any{
		"model": "deepseek-v4.1-flash",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "fix it"},
			}},
			map[string]any{"type": "custom_tool_call", "call_id": "call_1", "name": "apply_patch", "input": patch},
			map[string]any{"type": "custom_tool_call_output", "call_id": "call_1", "output": "ok"},
		},
		"tools": []any{
			map[string]any{"type": "custom", "name": "apply_patch", "description": "edit files"},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := PrepareResponsesBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(meta.Body, &body); err != nil {
		t.Fatal(err)
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) < 3 {
		t.Fatalf("messages=%v", msgs)
	}
	asst, _ := msgs[1].(map[string]any)
	calls, _ := asst["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("tool_calls=%v", asst)
	}
	fn := calls[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "apply_patch" {
		t.Fatalf("name=%v", fn)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(fn["arguments"].(string)), &args); err != nil {
		t.Fatalf("arguments not json: %v", fn["arguments"])
	}
	if args["input"] != patch {
		t.Fatalf("wrapped input=%q", args["input"])
	}
	tool, _ := msgs[2].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" || tool["content"] != "ok" {
		t.Fatalf("tool output=%v", tool)
	}
}

func TestEncodeResponsesJSONApplyPatchIsCustomToolCall(t *testing.T) {
	global.CORE_CONFIG.Gateway.Passthrough = true
	patch := "*** Begin Patch\n*** Add File: a.txt\n+hi\n*** End Patch"
	args, _ := json.Marshal(map[string]string{"input": patch})
	raw, err := encodeResponsesJSON(&ChatResult{
		ID:      "resp_1",
		Model:   "deepseek-v4.1-flash",
		Created: 1,
		ToolCalls: []AggregatedToolCall{
			{ID: "call_1", Name: "apply_patch", Arguments: string(args)},
			{ID: "call_2", Name: "exec_command", Arguments: `{"cmd":"ls"}`},
		},
		FinishReason: "tool_calls",
		Usage:        &parsedUsage{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	output, _ := resp["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("output=%v", output)
	}
	custom := output[0].(map[string]any)
	if custom["type"] != "custom_tool_call" || custom["name"] != "apply_patch" || custom["input"] != patch {
		t.Fatalf("custom=%v", custom)
	}
	if strings.HasPrefix(fmtString(custom["input"]), "{") {
		t.Fatalf("input still json wrapped: %v", custom["input"])
	}
	fn := output[1].(map[string]any)
	if fn["type"] != "function_call" || fn["name"] != "exec_command" {
		t.Fatalf("function=%v", fn)
	}
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}

func TestResponsesStreamApplyPatchEmitsCustomToolCall(t *testing.T) {
	global.CORE_CONFIG.Gateway.Passthrough = true
	patch := "*** Begin Patch\n*** Add File: a.txt\n+hi\n*** End Patch"
	var buf bytes.Buffer
	ad := newStreamAdapter(ProtocolResponses, &buf, nil, "deepseek-v4.1-flash")
	ad.start()
	ad.onToolCall(AggregatedToolCall{Index: 0, ID: "call_1", Name: "apply_patch", Arguments: `{"input":`})
	ad.onToolCall(AggregatedToolCall{Index: 0, Arguments: jsonString(patch) + "}"})
	ad.onFinishReason("tool_calls")
	if err := ad.finish(); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	encodedPatch := jsonString(patch)
	for _, ev := range []string{
		"event: response.output_item.added",
		"event: response.custom_tool_call_input.delta",
		"event: response.custom_tool_call_input.done",
		"event: response.output_item.done",
		`"type":"custom_tool_call"`,
		encodedPatch,
	} {
		if !strings.Contains(s, ev) {
			t.Fatalf("missing %s in %s", ev, s)
		}
	}
	if strings.Contains(s, `"type":"function_call"`) {
		t.Fatalf("apply_patch leaked as function_call: %s", s)
	}
	if strings.Contains(s, `"arguments":"{\"input\"`) || strings.Contains(s, `"input":"{\"input\"`) {
		t.Fatalf("custom input still json-wrapped: %s", s)
	}

	buf.Reset()
	ad = newStreamAdapter(ProtocolResponses, &buf, nil, "deepseek-v4.1-flash")
	ad.start()
	ad.onToolCall(AggregatedToolCall{Index: 0, ID: "call_2", Name: "exec_command", Arguments: `{"cmd":"ls"}`})
	ad.onFinishReason("tool_calls")
	if err := ad.finish(); err != nil {
		t.Fatal(err)
	}
	s = buf.String()
	if !strings.Contains(s, `"type":"function_call"`) || !strings.Contains(s, "event: response.function_call_arguments.delta") {
		t.Fatalf("exec_command should stay function_call: %s", s)
	}
	if strings.Contains(s, `"type":"custom_tool_call"`) {
		t.Fatalf("exec_command leaked as custom: %s", s)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestLooksLikePreamble(t *testing.T) {
	if !looksLikePreamble("I'll use apply_patch for all three changes. Note it failed earlier.") {
		t.Fatal("expected preamble")
	}
	if !looksLikePreamble("`loadAll` is now fault-tolerant. Next: the login button state") {
		t.Fatal("expected next-step preamble")
	}
	if looksLikePreamble("") || looksLikePreamble("PATCH_OK") {
		t.Fatal("final answers must not be nudged")
	}
	if looksLikePreamble(strings.Repeat("done. ", 200)) {
		t.Fatal("long answers must not be nudged")
	}
}

func TestNudgeChatBodyRequiresTools(t *testing.T) {
	raw, err := nudgeChatBody([]byte(`{"model":"x","messages":[{"role":"user","content":"fix it"}]}`), "I'll patch it")
	if err != nil {
		t.Fatal(err)
	}
	var chat map[string]any
	if err := json.Unmarshal(raw, &chat); err != nil {
		t.Fatal(err)
	}
	if chat["tool_choice"] != "required" {
		t.Fatalf("tool_choice=%v", chat["tool_choice"])
	}
	msgs := chat["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages=%v", msgs)
	}
}

func TestUnattendedRuntimeAndApplyPatchHint(t *testing.T) {
	global.CORE_CONFIG.Gateway = config.Gateway{Passthrough: true}
	meta, err := PrepareResponsesBody([]byte(`{
		"model":"deepseek-v4.1-flash",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"fix it"}]}],
		"tools":[{"type":"custom","name":"apply_patch","description":"edit files","format":{"type":"grammar","definition":"*** Begin Patch"}}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(meta.Body, &body); err != nil {
		t.Fatal(err)
	}
	fn := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	desc := fn["description"].(string)
	if !strings.Contains(desc, "*** Begin Patch") {
		t.Fatalf("grammar dropped: %s", desc)
	}
	found := false
	for _, item := range body["messages"].([]any) {
		m := item.(map[string]any)
		if m["role"] == "system" && strings.Contains(asString(m["content"]), "keep calling tools") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing unattended runtime note: %v", body["messages"])
	}
}
