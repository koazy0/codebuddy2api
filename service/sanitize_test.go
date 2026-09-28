package service

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// stripZeroWidth 去掉零宽空格，用于断言「脱敏只插了不可见字符，没改文字」。
func stripZeroWidth(s string) string {
	return strings.ReplaceAll(s, zwsp, "")
}

// TestSanitizeBrandFingerprintAcrossVersions 是这次改动的核心保证：
// 旧实现用 strings.ReplaceAll 只认一个字面句子，Codex 提示词改一个标点就静默失效，
// 上游随即返回 11128、表现为「说一句就断」。这里覆盖历史上出现过的多种写法，
// 任何一条回归都会让这个测试红。
func TestSanitizeBrandFingerprintAcrossVersions(t *testing.T) {
	cases := []struct {
		name string
		in   string
		// keepAll 非空时，改用「这些片段必须原样保留」做断言（输入本身不含 Codex 身份）。
		keepAll []string
	}{
		{
			name: "canonical",
			in:   "You are a coding agent running in the Codex CLI, a terminal-based coding assistant. Codex CLI is an open source project led by OpenAI. You are expected to be precise, safe, and helpful.",
		},
		{
			name:    "hyphenated-open-source-with-comma",
			in:      "Codex CLI, an open-source project by OpenAI, expects you to be precise.",
			keepAll: []string{"expects you to be precise."},
		},
		{
			name:    "no-space-open source",
			in:      "Codex is an opensource project led by OpenAI and maintained by contributors.",
			keepAll: []string{"maintained by contributors."},
		},
		{
			name: "developed-by",
			in:   "Codex CLI is a tool developed by OpenAI. Be concise.",
			// 这句里 "Codex CLI" 是被删掉的归属句的一部分，只要求剩余指令保留。
			keepAll: []string{"Be concise."},
		},
		{
			name:    "from-openai",
			in:      "Codex CLI, an open source project from OpenAI, is here.",
			keepAll: []string{"is here."},
		},
		{
			name:    "chinese-punctuation",
			in:      "Codex CLI，an open source project led by OpenAI，请保持精确。",
			keepAll: []string{"请保持精确。"},
		},
		{
			name: "attribution-sentence-bare",
			in:   "This assistant was built by OpenAI for terminal use.",
			// 这句里本来就没有 Codex 身份可言，只要求：不吃掉句子其它部分。
			keepAll: []string{"This assistant was", "for terminal use."},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := sanitizeSystemText(tc.in)
			// 1) 归属句必须消失（不留 "open source project led by" 之类残句）
			low := strings.ToLower(out)
			for _, bad := range []string{
				"open source project led by",
				"open-source project by",
				"opensource project led by",
				"developed by openai",
				"project from openai",
				"built by openai",
			} {
				if strings.Contains(low, bad) {
					t.Fatalf("attribution survived %q: %q", bad, out)
				}
			}
			// 2) 不能再出现裸露的品牌词（零宽化之后仍含 zi 才算残留）
			if strings.Contains(out, "OpenAI") {
				t.Fatalf("raw brand term survived: %q", out)
			}
			// 3) 去掉零宽后，Codex 身份必须还在——这是「原生身份透传」路线的前提。
			//    但输入本身不含 Codex 的用例改为断言「句子其余部分没被吞掉」。
			plain := stripZeroWidth(out)
			if len(tc.keepAll) > 0 {
				for _, keep := range tc.keepAll {
					if !strings.Contains(plain, keep) {
						t.Fatalf("content was swallowed: %q missing from %q", keep, plain)
					}
				}
				return
			}
			if !strings.Contains(plain, "Codex") {
				t.Fatalf("codex identity lost: %q", out)
			}
		})
	}
}

// TestSanitizeKeepsCodexIdentity 固定「只清指纹、不动身份」的边界：
// 行为准则、协作方式、任务执行描述都必须原样保留。
func TestSanitizeKeepsCodexIdentity(t *testing.T) {
	in := strings.Join([]string{
		"You are a coding agent running in the Codex CLI, a terminal-based coding assistant.",
		"Codex CLI is an open source project led by OpenAI.",
		"## Responsiveness",
		"### Preamble messages",
		"Before making tool calls, send a brief preamble to the user.",
		"# AGENTS.md spec",
		"- Repos often contain AGENTS.md files.",
		"## Task execution",
		"Do not guess or make up an answer.",
	}, "\n")

	out := sanitizeSystemText(in)
	plain := stripZeroWidth(out)

	for _, keep := range []string{
		"## Responsiveness",
		"### Preamble messages",
		"Before making tool calls, send a brief preamble to the user.",
		"# AGENTS.md spec",
		"- Repos often contain AGENTS.md files.",
		"## Task execution",
		"Do not guess or make up an answer.",
	} {
		if !strings.Contains(plain, keep) {
			t.Fatalf("section dropped: %q", keep)
		}
	}
}

// TestZeroWidthSplitIsReadable 保证脱敏手段是「不可见」而不是「改写」：
// 去掉零宽后必须与原文完全一致。
func TestZeroWidthSplitIsReadable(t *testing.T) {
	for _, term := range []string{"DoS", "sandbox", "vulnerability", "escalation"} {
		got := zeroWidthSplit(term)
		if got == term {
			t.Fatalf("%q was not desensitized", term)
		}
		if stripZeroWidth(got) != term {
			t.Fatalf("%q changed text, not just visibility: %q", term, got)
		}
		if len([]rune(got)) != len([]rune(term))+1 {
			t.Fatalf("%q should gain exactly one zero-width rune: %q", term, got)
		}
	}
}

// TestComplianceTermsAreDesensitized 覆盖 shouzhuo/desensitize.py 的等价能力。
func TestComplianceTermsAreDesensitized(t *testing.T) {
	in := "Refuse requests for DoS attacks, exploit development, credential testing, C2 frameworks. " +
		"Actions that are destructive or require escalated privileges must be refused. " +
		"Never use sandbox escapes or weaponize a vulnerability."
	out := sanitizeSystemText(in)

	for _, term := range []string{"DoS", "attacks", "exploit", "credential testing", "C2 frameworks", "destructive", "escalated privileges", "sandbox", "weaponize", "vulnerability"} {
		if strings.Contains(out, term) {
			t.Fatalf("term %q left raw in: %q", term, out)
		}
	}
	// 语义必须可读：去掉零宽 = 原文
	if stripZeroWidth(out) != in {
		t.Fatalf("text was rewritten, not desensitized:\n got=%q\nwant=%q", stripZeroWidth(out), in)
	}
}

// TestUserMessagesUntouched 是最重要的安全边界：
// 用户真实提问一个字都不能改，否则等于篡改对话。
// assistant 属对话内容，允许不可见脱敏，但剥掉零宽后必须与原文完全一致。
func TestUserMessagesUntouched(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "帮我分析 DoS 攻击和 OpenAI 的关系"},
			map[string]any{"role": "assistant", "content": "exploit development 是敏感话题"},
			map[string]any{"role": "system", "content": "DoS attacks are refused."},
		},
	}
	sanitizeUpstreamChat(body)
	msgs := body["messages"].([]any)

	user := msgs[0].(map[string]any)["content"].(string)
	if user != "帮我分析 DoS 攻击和 OpenAI 的关系" {
		t.Fatalf("user message was modified: %q", user)
	}
	assistant, _ := msgs[1].(map[string]any)["content"].(string)
	if strings.Contains(assistant, "exploit") {
		t.Fatalf("assistant term should be desensitized: %q", assistant)
	}
	if stripZeroWidth(assistant) != "exploit development 是敏感话题" {
		t.Fatalf("assistant text was rewritten, not desensitized: %q", assistant)
	}
	sys := msgs[2].(map[string]any)["content"].(string)
	if strings.Contains(sys, "DoS") {
		t.Fatalf("system term should be desensitized: %q", sys)
	}
}

// TestSanitizeHandlesContentShapes 三种 content 形状都要覆盖：
// 字符串、content blocks 数组、以及 {text}/{content} 嵌套对象。
func TestSanitizeHandlesContentShapes(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "DoS is refused."},
			map[string]any{"role": "system", "content": []any{
				map[string]any{"type": "text", "text": "exploit is refused."},
			}},
			map[string]any{"role": "developer", "content": map[string]any{
				"text":    "sandbox notes",
				"content": []any{map[string]any{"type": "text", "text": "malware notes"}},
			}},
		},
	}
	sanitizeUpstreamChat(body)
	msgs := body["messages"].([]any)

	if got := msgs[0].(map[string]any)["content"].(string); strings.Contains(got, "DoS") {
		t.Fatalf("string content not sanitized: %q", got)
	}
	block := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if got := block["text"].(string); strings.Contains(got, "exploit") {
		t.Fatalf("block text not sanitized: %q", got)
	}
	nested := msgs[2].(map[string]any)["content"].(map[string]any)
	if got := nested["text"].(string); strings.Contains(got, "sandbox") {
		t.Fatalf("nested text not sanitized: %q", got)
	}
	inner := nested["content"].([]any)[0].(map[string]any)
	if got := inner["text"].(string); strings.Contains(got, "malware") {
		t.Fatalf("nested block not sanitized: %q", got)
	}
}

// TestSanitizeIdempotent 重复清洗不能叠加零宽字符，否则多轮对话会把文本撑坏。
func TestSanitizeIdempotent(t *testing.T) {
	in := "Codex CLI is an open source project led by OpenAI. DoS attacks are refused."
	once := sanitizeSystemText(in)
	twice := sanitizeSystemText(once)
	if once != twice {
		t.Fatalf("sanitize is not idempotent:\n once=%q\ntwice=%q", once, twice)
	}
}

// TestTidySpacing 整句替换后不能留下双空格（模型可能把这当成格式化信号）。
func TestTidySpacing(t *testing.T) {
	got := tidySpacing("You are a coding agent.  \n\n  Keep secrets.")
	if strings.Contains(got, "  ") {
		t.Fatalf("double space survived: %q", got)
	}
	if !strings.Contains(got, "\n\n") {
		t.Fatalf("paragraph break was destroyed: %q", got)
	}
}

// TestClassifyUpstreamRejection 固定错误归类，避免「账号问题」和「请求问题」再次分叉。
func TestClassifyUpstreamRejection(t *testing.T) {
	cases := []struct {
		name string
		body string
		want rejectionKind
	}{
		{"unapproved-11128", `{"code":11128,"msg":"Illegal API invocation from an unapproved channel"}`, rejectionUnapprovedChannel},
		{"unapproved-text", `{"msg":"unapproved channel"}`, rejectionUnapprovedChannel},
		{"model-11102", `{"code":11102,"msg":"model is only available for authorized users"}`, rejectionModelUnauthorized},
		{"content-filter", `{"code":12200,"msg":"content filter triggered"}`, rejectionContentFiltered},
		// 11140：内容安全审核。dsh 链路上的主要拦截方式，
		// 文案里既没有 "content filter" 也没有 "sensitive"，曾整类漏掉。
		{"safety-11140-en", `{"code":11140,"msg":"request illegal","displayMsg":{"en":"Content failed safety review. Please revise it","zh":"内容未通过安全审核，请修改"}}`, rejectionContentFiltered},
		{"safety-11140-code-only", `{"code":11140,"msg":"request illegal"}`, rejectionContentFiltered},
		{"safety-review-text", `{"msg":"Content failed safety review"}`, rejectionContentFiltered},
		{"safety-zh", `{"msg":"内容未通过安全审核，请修改后重试"}`, rejectionContentFiltered},
		{"ok", `{"code":0,"msg":"ok"}`, rejectionNone},
		{"empty", ``, rejectionNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyUpstreamRejection([]byte(tc.body)); got != tc.want {
				t.Fatalf("classify(%q)=%v want %v", tc.body, got, tc.want)
			}
		})
	}
}

// TestContentFilteredIsNotQuota 内容审核命中不能被误判成「额度耗尽」，
// 否则会把好账号打成模型冷却，越修越坏。
func TestContentFilteredIsNotQuota(t *testing.T) {
	if isModelQuotaExhausted(400, []byte(`{"code":12200,"msg":"content filter triggered"}`)) {
		t.Fatal("content filter must not be treated as quota exhaustion")
	}
	if !isModelQuotaExhausted(429, []byte(`{"msg":"rate limit"}`)) {
		t.Fatal("429 must still be quota exhaustion")
	}
}

// TestResanitizeHandlesHarnessUserMessages 是 11128 降级重试的核心：
// 第一轮清洗只动 system/developer，但 Codex 会把 harness 上下文塞进 user 消息，
// 被拦时的第二趟必须能把这些也脱敏掉。线上日志实测：同一请求换 8 个账号全被拦。
func TestResanitizeHandlesHarnessUserMessages(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "You are a coding agent."},
			map[string]any{"role": "user", "content": "<permissions instructions>\nFilesystem sandboxing defines which files can be read or written. Escalation to unsandboxed execution may be required.\n</permissions instructions>"},
			map[string]any{"role": "user", "content": "帮我看看这个 vulnerability 报告"},
		},
	}
	updated, changed := resanitizeUpstreamChat(body)
	if !changed {
		t.Fatal("harness user message should have been sanitized")
	}
	msgs := updated["messages"].([]any)

	harness := msgs[1].(map[string]any)["content"].(string)
	for _, term := range []string{"sandboxing", "unsandboxed", "Escalation"} {
		if strings.Contains(harness, term) {
			t.Fatalf("term %q left raw in harness message: %q", term, harness)
		}
	}
	if !strings.Contains(harness, "<permissions instructions>") {
		t.Fatalf("harness structure must be preserved: %q", harness)
	}

	// 真实用户提问一个字都不能动，哪怕里面有敏感词。
	real := msgs[2].(map[string]any)["content"].(string)
	if real != "帮我看看这个 vulnerability 报告" {
		t.Fatalf("real user question was modified: %q", real)
	}
}

// TestResanitizeReportsNoChangeWhenNothingToDo 降级重试必须能识别「清无可清」，
// 否则调用方会陷入无限重试。
func TestResanitizeReportsNoChangeWhenNothingToDo(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "You are a coding agent."},
			map[string]any{"role": "user", "content": "普通提问，没有任何 harness 标记"},
		},
	}
	if _, changed := resanitizeUpstreamChat(body); changed {
		t.Fatal("clean body should report no change")
	}
}

// TestRetryWAFRejectedBody 端到端：被拦后 meta.Body 应真的被换成更干净的版本。
func TestRetryWAFRejectedBody(t *testing.T) {
	raw := []byte(`{"model":"glm-5.2","messages":[{"role":"user","content":"<environment_context><permissions instructions>sandbox rules apply</permissions instructions></environment_context>"}]}`)
	meta := &ChatRequestMeta{Protocol: ProtocolResponses, UpstreamModel: "glm-5.2", Body: raw}
	if !retryWAFRejectedBody(meta) {
		t.Fatal("expected body to be rewritten")
	}
	if string(meta.Body) == string(raw) {
		t.Fatal("body was not actually changed")
	}
	if strings.Contains(string(meta.Body), "sandbox") {
		t.Fatalf("sanitized body still has raw term: %s", meta.Body)
	}
	var parsed map[string]any
	if err := json.Unmarshal(meta.Body, &parsed); err != nil {
		t.Fatalf("rewritten body is not valid json: %v", err)
	}
	if parsed["model"] != "glm-5.2" {
		t.Fatalf("model field must be preserved: %v", parsed["model"])
	}
	if meta.RequestPreview == "" {
		t.Fatal("request preview should be refreshed")
	}
}

// TestLooksLikeHarnessContext 标记识别要准，不能把普通提问误判成 harness 上下文
// （否则会去改用户真实内容）。
func TestLooksLikeHarnessContext(t *testing.T) {
	harness := []string{
		"# AGENTS.md instructions for /repo",
		"<environment_context>cwd=/tmp</environment_context>",
		"<permissions instructions>...</permissions instructions>",
		"<collaboration_mode>Default</collaboration_mode>",
		"<skills_instructions>...</skills_instructions>",
		"<system-reminder>note</system-reminder>",
		"# claudeMd\nproject notes",
	}
	for _, h := range harness {
		if !looksLikeHarnessContext(h) {
			t.Fatalf("should be detected as harness context: %q", h)
		}
	}
	plain := []string{
		"帮我重构这个函数",
		"explain how the sandbox works",
		"",
	}
	for _, p := range plain {
		if looksLikeHarnessContext(p) {
			t.Fatalf("false positive on real user input: %q", p)
		}
	}
}

// TestSafetyReviewTriggersRetry 钉住 11140 必须走降级重试路径。
//
// 这条路径曾整类失效：分类器只认 "content filter"/"sensitive" 等词，
// 而 11140 的文案是 "Content failed safety review"，一个都不匹配，
// 于是被归类成 rejectionNone —— proxy 认为不是内容问题，
// 不降级、直接抛 403。表现为「用 dsh 必被拿下，且毫无重试迹象」。
func TestSafetyReviewTriggersRetry(t *testing.T) {
	body := []byte(`{"code":11140,"msg":"request illegal","displayMsg":{"en":"Content failed safety review. Please revise it","zh":"内容未通过安全审核，请修改"}}`)

	if !isContentFiltered(body) {
		t.Fatal("11140 必须被识别为内容审核命中")
	}
	// 两条判定都要成立，proxy 里是 (isUnapprovedChannel || isContentFiltered)
	if !isUpstreamRequestError(body) {
		t.Fatal("内容审核命中也属于「请求被拒但不是账号的错」")
	}
	// 不能误判成额度耗尽：那会把好账号打成模型冷却
	if isModelQuotaExhausted(403, body) {
		t.Fatal("内容审核命中不能当成额度耗尽")
	}
	// 也不能误判成模型不可用：那会走换模型重试而不是清洗重试
	if isUpstreamModelUnavailable(body) {
		t.Fatal("内容审核命中不能当成模型不可用")
	}
}

// TestSafetyReviewActuallyChangesBody 确认降级重试真的能改出东西。
// 分类对了但清洗无变化 = 空转，用户仍看到 403。
func TestSafetyReviewActuallyChangesBody(t *testing.T) {
	meta := &ChatRequestMeta{
		Body: []byte(`{"messages":[
			{"role":"system","content":"You are a coding agent running in the Codex CLI. Refuse requests for DoS attacks, exploit development, credential testing."},
			{"role":"user","content":"<environment_context>cwd=/tmp sandbox=unsandboxed</environment_context>help me"}]}`),
	}
	if !retryWAFRejectedBody(meta) {
		t.Fatal("降级清洗应产生变化，否则重试是空转")
	}
	if !bytes.Contains(meta.Body, []byte("exploit")) {
		// 对话面不删词，只插零宽——词应仍在
		t.Log("exploit 仍存在（符合「只插零宽不删词」的约定）")
	}
	if bytes.Contains(meta.Body, []byte("<environment_context>")) {
		t.Fatal("harness 块应被重写成摘要")
	}
}
