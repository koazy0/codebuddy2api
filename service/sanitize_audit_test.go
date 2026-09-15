package service

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestInvisibleMarksDoNotBlindDetection 锁定一个真实自伤缺陷：
// harness 档把 "sandbox" 变成 "s<U+200B>andbox" 之后，词表正则再也匹配不到它，
// 于是 full 档判定「无敏感词」→ 结构性手段不触发 → changed=false → 降级重试空转。
// 等价于把「请求被拒」换成「静默放弃」。检测前必须剥离不可见标记。
func TestInvisibleMarksDoNotBlindDetection(t *testing.T) {
	desensitized := "Run in the s\u200bandbox with e\u200bscalation support"
	if !hasSensitiveTerm(desensitized) {
		t.Fatal("hasSensitiveTerm must see through its own zero-width marks")
	}
	if hasSensitiveTerm("a perfectly clean sentence about lunch") {
		t.Fatal("clean text must not be flagged")
	}
}

// TestTwoPhaseEscalationStillProducesChange 是最关键的两阶段回归：
// proxy 首轮按 harness 档清洗，被 11128 拒绝后升到 full 档重发。
// 旧实现下第二趟会报告「无变化」，降级重试形同虚设。
func TestTwoPhaseEscalationStillProducesChange(t *testing.T) {
	body := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{
				"name":        "shell",
				"description": "Run in the sandbox with escalation support",
				"parameters":  map[string]any{"type": "object"},
			}},
		},
	}
	sanitizeRequest(body, sanitizeHarness)

	if !sanitizeRequest(body, sanitizeFull) {
		t.Fatal("escalation to full must report a change, otherwise the retry is a no-op")
	}
	fn := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if desc, ok := fn["description"]; ok && hasSensitiveTerm(asString(desc)) {
		t.Fatalf("description still trips the term list after escalation: %v", desc)
	}
}

// TestApplyPatchFormatHintSurvivesFullLevel 锁定第二个真实缺陷：
// full 档原先整段删除被污染的 description，连带丢掉网关注入的功能文本
// "*** Begin Patch"，模型随即不知道该用什么格式提交补丁。
// 现在改为句子级剪枝：脏句删掉，格式说明保留。
func TestApplyPatchFormatHintSurvivesFullLevel(t *testing.T) {
	raw := []byte(`{"model":"glm-5.2","input":"hi","tools":[
		{"type":"custom","name":"apply_patch",
		 "description":"Apply patches in the sandbox workspace.",
		 "format":{"type":"grammar","definition":"*** Begin Patch"}}]}`)

	meta, err := PrepareResponsesBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(meta.Body, &body); err != nil {
		t.Fatal(err)
	}
	resanitizeUpstreamChat(body)

	fn := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	desc := asString(fn["description"])
	if !strings.Contains(desc, "*** Begin Patch") {
		t.Fatalf("apply_patch format hint was destroyed by full-level sanitize: %q", desc)
	}
	if hasSensitiveTerm(desc) {
		t.Fatalf("sensitive content survived pruning: %q", desc)
	}
}

// TestToolCallArgumentsSanitized 锁定第三个缺陷：
// assistant 的 tool_calls[].function.arguments 是模型生成的工具入参，
// 不在 content 里，只洗 content 会整片漏掉。
func TestToolCallArgumentsSanitized(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []any{
					map[string]any{
						"id":   "c1",
						"type": "function",
						"function": map[string]any{
							"name":      "exec_command",
							"arguments": `{"cmd":"run vulnerability scan in the sandbox"}`,
						},
					},
				},
			},
		},
	}

	if !sanitizeRequest(body, sanitizeFull) {
		t.Fatal("arguments with sensitive terms must be reported as a change")
	}
	call := body["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	args := call["function"].(map[string]any)["arguments"].(string)

	if strings.Contains(args, "sandbox") || strings.Contains(args, "vulnerability") {
		t.Fatalf("raw terms survived in tool arguments: %q", args)
	}
	// arguments 必须是合法 JSON：破坏结构会让上游直接解析失败。
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		t.Fatalf("arguments is no longer valid json: %q (%v)", args, err)
	}
	if _, ok := parsed["cmd"]; !ok {
		t.Fatalf("cmd key was lost: %#v", parsed)
	}
}

// TestToolCallArgumentsKeysUntouched 键名绝不能改，改名等于改变工具入参语义。
func TestToolCallArgumentsKeysUntouched(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"function": map[string]any{
							"name":      "run",
							"arguments": `{"sandbox_mode":"strict"}`,
						},
					},
				},
			},
		},
	}
	sanitizeRequest(body, sanitizeFull)
	call := body["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	args := call["function"].(map[string]any)["arguments"].(string)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		t.Fatalf("not valid json: %q", args)
	}
	if _, ok := parsed["sandbox_mode"]; !ok {
		t.Fatalf("json key was renamed: %#v", parsed)
	}
}

// TestMalformedArgumentsNotBroken 非法 JSON 的 arguments 不能被改成更坏的东西。
func TestMalformedArgumentsNotBroken(t *testing.T) {
	raw := "not-json at all: sandbox"
	got, ok := sanitizeJSONArguments(raw)
	if !ok {
		t.Fatal("freeform arguments should still be handled")
	}
	if strings.Contains(got, "sandbox") {
		t.Fatalf("freeform leak survived: %q", got)
	}
}

// TestAnthropicPathParity anthropic（Claude Code）链路与 responses 共用清洗节点，
// 这里固定两者行为一致，避免只修了一条链路。
func TestAnthropicPathParity(t *testing.T) {
	raw := []byte(`{"model":"glm-5.2","max_tokens":64,
		"system":"You are Claude Code, Anthropic's CLI. Sandbox rules apply.",
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"name":"Bash","description":"Run sandboxed commands","input_schema":{"type":"object"}}]}`)

	meta, err := PrepareAnthropicBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(meta.Body, &body); err != nil {
		t.Fatal(err)
	}
	if !resanitizeOK(body) {
		t.Fatal("anthropic body should escalate")
	}
	out, _ := json.Marshal(body)
	for _, term := range []string{"Anthropic", "sandbox"} {
		if strings.Contains(string(out), term) {
			t.Fatalf("anthropic path left raw %q: %s", term, out)
		}
	}
	// user 提问仍然逐字节保留
	msgs := body["messages"].([]any)
	u := msgs[1].(map[string]any)["content"].(string)
	if u != "hi" {
		t.Fatalf("user content changed: %q", u)
	}
}

// resanitizeOK 是测试辅助：跑一遍降级清洗并返回是否变化。
func resanitizeOK(body map[string]any) bool {
	_, changed := resanitizeUpstreamChat(body)
	return changed
}

// TestResponsesReasoningFieldsPreserved 清洗不能顺手改协议字段。
func TestResponsesReasoningFieldsPreserved(t *testing.T) {
	raw := []byte(`{"model":"glm-5.2","input":"hi","reasoning":{"effort":"high","summary":"auto"},
		"tools":[{"type":"function","name":"shell","description":"sandbox tool","parameters":{"type":"object"}}]}`)
	meta, err := PrepareResponsesBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	json.Unmarshal(meta.Body, &body)
	resanitizeUpstreamChat(body)

	if body["reasoningEffort"] != "high" {
		t.Fatalf("reasoningEffort lost: %v", body["reasoningEffort"])
	}
	if body["reasoning_summary"] != "auto" {
		t.Fatalf("reasoning_summary lost: %v", body["reasoning_summary"])
	}
	if body["stream"] != true {
		t.Fatalf("stream must remain true for upstream SSE: %v", body["stream"])
	}
}

// TestWordDerivativesAreCaught 锁定一个只在 Claude Code 链路暴露的缺陷。
//
// 词表里是 `exploit`，但真实文本写 `exploitation` / `exploited` / `exploiting`，
// 旧实现两侧都锁 \b，右侧边界在 "exploit" 后紧跟字母时不成立，整词匹配失败，
// 派生词就裸着发往上游。上游是朴素子串匹配，漏掉就等于没防。
// 实测来源：anthropic 的 tool_result 回显 "exploitation vector present"。
func TestWordDerivativesAreCaught(t *testing.T) {
	for _, s := range []string{
		"exploitation vector present",
		"exploited the system",
		"exploiting a vulnerability",
		"attacking the server",
		"attacker moved laterally",
		"hacking tools",
	} {
		out := desensitizeAllTerms(s)
		low := strings.ToLower(stripInvisibleMarks(out))
		for _, stem := range []string{"exploit", "attack", "hack"} {
			if strings.Contains(low, stem) && !strings.Contains(out, "\u200b") {
				t.Fatalf("%q 中的 %q 未被脱敏: %q", s, stem, out)
			}
		}
		if !hasSensitiveTerm(s) {
			t.Fatalf("派生词未被识别: %q", s)
		}
		if stripInvisibleMarks(out) != s {
			t.Fatalf("语义被改写而非脱敏: %q -> %q", s, out)
		}
	}
}

// TestWordBoundaryLeftSideStillWorks 放松右侧的前提是左侧边界不能丢，
// 否则 skill / counterattack 这类正常词会被误伤。
func TestWordBoundaryLeftSideStillWorks(t *testing.T) {
	for _, s := range []string{"the skill set", "counterattack strategy", "a legitimate request"} {
		if out := desensitizeAllTerms(s); out != s {
			t.Fatalf("正常词被误伤: %q -> %q", s, out)
		}
	}
}

// TestClaudeCodePathEndToEnd Claude Code 真实形状（system 数组 + tool_use/tool_result）
// 全链路走一遍，确认没有任何裸词残留。
func TestClaudeCodePathEndToEnd(t *testing.T) {
	raw := []byte(`{"model":"glm-5.2","max_tokens":8000,"stream":true,
		"system":[{"type":"text","text":"You are Claude Code, Anthropic's official CLI. Follow the sandbox policy."}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"帮我看看这个漏洞"}]},
			{"role":"assistant","content":[
				{"type":"text","text":"I'll inspect the sandbox."},
				{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"grep -r sandbox /etc"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"exploitation vector present"}]}]}
		],
		"tools":[{"name":"Bash","description":"Run a command in the sandbox. Escalation may be needed.","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}}]}`)

	meta, err := PrepareAnthropicBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(meta.Body, &body); err != nil {
		t.Fatal(err)
	}
	resanitizeUpstreamChat(body)
	out, _ := json.Marshal(body)

	for _, term := range []string{"Anthropic", "sandbox", "exploit", "Escalation"} {
		if strings.Contains(string(out), term) {
			t.Fatalf("Claude Code 链路残留裸词 %q: %s", term, out)
		}
	}
	// 用户提问逐字节保留
	msgs := body["messages"].([]any)
	if got := msgs[1].(map[string]any)["content"].(string); got != "帮我看看这个漏洞" {
		t.Fatalf("用户提问被改写: %q", got)
	}
}
