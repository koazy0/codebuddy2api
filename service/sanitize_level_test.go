package service

import (
	"encoding/json"
	"strings"
	"testing"
)

// mustBody 是测试里的取字段工具：把 body 序列化再解回来，
// 确保断言看到的是「真正会发往上游的字节」，而不是内存里未被序列化的中间态。
func mustBody(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return out
}

func firstMessage(t *testing.T, body map[string]any, idx int) map[string]any {
	t.Helper()
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) <= idx {
		t.Fatalf("messages[%d] missing: %#v", idx, body["messages"])
	}
	m, _ := msgs[idx].(map[string]any)
	if m == nil {
		t.Fatalf("messages[%d] is not an object", idx)
	}
	return m
}

// TestToolDescriptionSanitizedAtHarnessLevel 覆盖旧实现完全遗漏的一环：
// tool 定义以前是原样进请求体的，description 里的 sandbox / escalation
// 直接命中上游审核。harness 档只做不可见脱敏，结构字段必须一个不差地留下。
func TestToolDescriptionSanitizedAtHarnessLevel(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
		},
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "shell",
					"description": "Run a command. May require escalation to unsandboxed execution.",
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"cmd": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
	}

	if !sanitizeRequest(body, sanitizeHarness) {
		t.Fatal("tool description with sensitive terms should be reported as a change")
	}

	out := mustBody(t, body)
	tools := out["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)

	desc := fn["description"].(string)
	for _, term := range []string{"escalation", "unsandboxed"} {
		if strings.Contains(desc, term) {
			t.Fatalf("raw term %q survived in tool description: %q", term, desc)
		}
	}
	// 去掉零宽后必须与原文逐字节一致：脱敏不是改写。
	if stripZeroWidth(desc) != "Run a command. May require escalation to unsandboxed execution." {
		t.Fatalf("description text was rewritten, not desensitized: %q", desc)
	}
	// 结构字段是工具调用的契约，任何档位都不能动。
	if fn["name"] != "shell" {
		t.Fatalf("tool name changed: %v", fn["name"])
	}
	if _, ok := fn["parameters"].(map[string]any); !ok {
		t.Fatalf("parameters were damaged: %#v", fn["parameters"])
	}
}

// TestToolDescriptionDroppedAtFullLevel 验证结构性手段：
// 走到 full 档说明零宽已经被上游识破，此时整段摘掉风险描述，
// 宁可让模型少一段说明，也不能让整条请求被拒。
func TestToolDescriptionDroppedAtFullLevel(t *testing.T) {
	body := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "shell",
					"description": "May require escalation.",
					"title":       "Sandboxed shell",
					"parameters":  map[string]any{"type": "object"},
				},
			},
		},
	}

	sanitizeRequest(body, sanitizeFull)
	out := mustBody(t, body)
	fn := out["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)

	if _, ok := fn["description"]; ok {
		t.Fatalf("description should be dropped at full level: %v", fn["description"])
	}
	if _, ok := fn["title"]; ok {
		t.Fatalf("title should be dropped at full level: %v", fn["title"])
	}
	if fn["name"] != "shell" {
		t.Fatalf("tool name must survive: %v", fn["name"])
	}
}

// TestToolStructuralFieldsUntouched 是最容易踩的坑：把 name / enum / default
// 也一并「脱敏」，模型就会返回对不上 schema 的参数，把「请求被拒」换成
// 「工具调用崩掉」。这些字段必须逐字节保留。
func TestToolStructuralFieldsUntouched(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"mode": map[string]any{
				"type":    "string",
				"enum":    []any{"sandbox", "escalated"},
				"default": "sandbox",
			},
		},
	}
	body := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools": []any{
			map[string]any{
				"type":     "function",
				"function": map[string]any{"name": "run", "parameters": params},
			},
		},
	}

	sanitizeRequest(body, sanitizeFull)
	out := mustBody(t, body)
	fn := out["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	got := fn["parameters"].(map[string]any)["properties"].(map[string]any)["mode"].(map[string]any)

	enum := got["enum"].([]any)
	if enum[0] != "sandbox" || enum[1] != "escalated" {
		t.Fatalf("enum was modified: %#v", enum)
	}
	if got["default"] != "sandbox" {
		t.Fatalf("default was modified: %v", got["default"])
	}
}

// TestAssistantCoveredOnlyAtFullLevel 锁定「跑一会儿才断」的成因：
// agentic 会话每轮重发全历史，assistant 正文里的 sandbox / escalation
// 会随轮次累积。默认档即做不可见脱敏（否则用户会感知到中途被拒），
// 但assistant 属真实对话内容，任何档位都不允许删改文字。
func TestAssistantCoveredOnlyAtFullLevel(t *testing.T) {
	newBody := func() map[string]any {
		return map[string]any{
			"messages": []any{
				map[string]any{"role": "system", "content": "You are helpful."},
				map[string]any{
					"role":    "assistant",
					"content": "I will run this in the sandbox after escalation.",
				},
			},
		}
	}

	for _, level := range []sanitizeLevel{sanitizeHarness, sanitizeFull} {
		body := newBody()
		sanitizeRequest(body, level)
		got := firstMessage(t, body, 1)["content"].(string)
		for _, term := range []string{"sandbox", "escalation"} {
			if strings.Contains(got, term) {
				t.Fatalf("level %v left raw %q in assistant history: %q", level, term, got)
			}
		}
		// 只允许不可见改写：剥掉零宽后必须与原文逐字节一致。
		if stripZeroWidth(got) != "I will run this in the sandbox after escalation." {
			t.Fatalf("level %v rewrote assistant text instead of desensitizing: %q", level, got)
		}
	}
}

// TestToolOutputCoveredAtFullLevel tool 输出（命令回显、文件内容、报错栈）
// 同样是不可控文本源且会随轮次累积，默认档即做不可见脱敏；
// 但它是真实交互记录，任何档位都不允许删改文字。
func TestToolOutputCoveredAtFullLevel(t *testing.T) {
	newBody := func() map[string]any {
		return map[string]any{
			"messages": []any{
				map[string]any{
					"role":         "tool",
					"tool_call_id": "call_1",
					"content":      "grep: found sandbox and vulnerability keywords",
				},
			},
		}
	}

	for _, level := range []sanitizeLevel{sanitizeHarness, sanitizeFull} {
		body := newBody()
		sanitizeRequest(body, level)
		got := firstMessage(t, body, 0)["content"].(string)
		for _, term := range []string{"sandbox", "vulnerability"} {
			if strings.Contains(got, term) {
				t.Fatalf("level %v left raw %q in tool output: %q", level, term, got)
			}
		}
		if stripZeroWidth(got) != "grep: found sandbox and vulnerability keywords" {
			t.Fatalf("level %v rewrote tool output instead of desensitizing: %q", level, got)
		}
	}
}

// TestRealUserQuestionNeverRewritten 是不可退让的边界，任何档位都必须成立：
// 用户真实提问一个字都不能改，哪怕里面全是敏感词。
func TestRealUserQuestionNeverRewritten(t *testing.T) {
	question := "帮我写一个 DoS 攻击和 sandbox escape 的漏洞利用分析"
	for _, level := range []sanitizeLevel{sanitizeHarness, sanitizeFull} {
		body := map[string]any{
			"messages": []any{
				map[string]any{"role": "user", "content": question},
			},
		}
		sanitizeRequest(body, level)
		if got := firstMessage(t, body, 0)["content"].(string); got != question {
			t.Fatalf("level %v rewrote the real user question: %q", level, got)
		}
	}
}

// TestHarnessBlockRewrittenAtFullLevel 验证不依赖零宽的那一层：
// 整块换成中性摘要，摘要自身不含任何敏感词。
func TestHarnessBlockRewrittenAtFullLevel(t *testing.T) {
	in := "<permissions instructions>\n" +
		"Filesystem sandboxing defines which files can be read or written. " +
		"Escalation to unsandboxed execution may be required.\n" +
		"</permissions instructions>"

	got := sanitizeText(in, sanitizeFull)
	for _, term := range []string{"sandboxing", "unsandboxed", "Escalation"} {
		if strings.Contains(got, term) {
			t.Fatalf("raw %q survived block rewrite: %q", term, got)
		}
	}
	if !strings.Contains(got, "<permissions instructions>") ||
		!strings.Contains(got, "</permissions instructions>") {
		t.Fatalf("markers must be preserved for prompt structure: %q", got)
	}
	// 替换文本本身不能带敏感词，否则等于没换。
	if hasSensitiveTerm(got) {
		t.Fatalf("replacement still trips the term list: %q", got)
	}
}

// TestHarnessBlockRewriteKeepsSurroundingText 块重写不能越界吞掉正文。
func TestHarnessBlockRewriteKeepsSurroundingText(t *testing.T) {
	in := "before text\n<environment_context>\ncwd=/tmp sandbox\n</environment_context>\nafter text"
	got := sanitizeText(in, sanitizeFull)

	if !strings.Contains(got, "before text") || !strings.Contains(got, "after text") {
		t.Fatalf("surrounding text was swallowed: %q", got)
	}
	if strings.Contains(got, "cwd=/tmp") {
		t.Fatalf("block body survived: %q", got)
	}
}

// TestFullLevelIdempotent 重复清洗不能叠加零宽字符，否则多轮会话会把文本撑坏，
// 也会让「这一趟到底改没改」的判断失真。
func TestFullLevelIdempotent(t *testing.T) {
	in := "System says: sandbox and vulnerability. <permissions instructions>escalation</permissions instructions>"
	once := sanitizeText(in, sanitizeFull)
	twice := sanitizeText(once, sanitizeFull)
	if once != twice {
		t.Fatalf("full-level sanitize is not idempotent:\nonce=%q\ntwice=%q", once, twice)
	}
}

// TestToolOnlyChangeIsReported 是这次修复的核心回归。
//
// 旧实现只遍历 messages，命中 11128 时若敏感词在 tools 里，
// 降级重试会判定「没有可改之处」而直接放弃——线上表现就是
// 反复看到 codebuddy_gateway_error，且日志写着 produced no change。
func TestToolOnlyChangeIsReported(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "普通提问"},
		},
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "shell",
					"description": "Run sandboxed commands with escalation",
					"parameters":  map[string]any{"type": "object"},
				},
			},
		},
	}

	updated, changed := resanitizeUpstreamChat(body)
	if !changed {
		t.Fatal("a body whose only leak is in tools must still report a change")
	}
	encoded, err := json.Marshal(updated)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "sandboxed") {
		t.Fatalf("tool description leak survived full-level retry: %s", encoded)
	}
}

// TestSanitizeOffIsNoop 排障开关必须真的关掉，方便对比线上现象。
func TestSanitizeOffIsNoop(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "sandbox and OpenAI"},
		},
		"tools": []any{
			map[string]any{
				"type":     "function",
				"function": map[string]any{"name": "shell", "description": "sandbox"},
			},
		},
	}
	if sanitizeRequest(body, sanitizeOff) {
		t.Fatal("sanitizeOff must not report a change")
	}
	if got := firstMessage(t, body, 0)["content"].(string); got != "sandbox and OpenAI" {
		t.Fatalf("sanitizeOff rewrote content: %q", got)
	}
}

// TestSanitizeLevelFromName 固定配置解析：未知值回落到默认档，不能「静默失效」。
func TestSanitizeLevelFromName(t *testing.T) {
	cases := map[string]sanitizeLevel{
		"":         sanitizeHarness,
		"harness":  sanitizeHarness,
		"full":     sanitizeFull,
		"FULL":     sanitizeFull,
		"off":      sanitizeOff,
		"nonsense": sanitizeHarness,
	}
	for in, want := range cases {
		if got := sanitizeLevelFromName(in); got != want {
			t.Fatalf("sanitizeLevelFromName(%q)=%v want %v", in, got, want)
		}
	}
}

// TestSanitizeModeFromConfigShape 用真实配置形状过一遍端到端，
// 确保 proxy 读的字段名与 YAML 里的 sanitize-mode 对得上。
func TestSanitizeModeFromConfigShape(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "OpenAI sandbox"},
		},
	}
	if !sanitizeRequest(body, sanitizeLevelFromName("full")) {
		t.Fatal("full mode should produce a change on this body")
	}
	out := mustBody(t, body)
	sys := firstMessage(t, out, 0)["content"].(string)
	if strings.Contains(sys, "OpenAI") {
		t.Fatalf("brand term survived in full mode: %q", sys)
	}
}
