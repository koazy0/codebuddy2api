package service

import (
	"strings"
	"testing"
	"time"

	"codebuddy-gateway/global"

	"go.uber.org/zap"
)

func setupPromptTest(t *testing.T) func() {
	t.Helper()
	old := global.CORE_CONFIG.Gateway
	oldLog := global.CORE_LOG
	global.CORE_LOG = zapNop()
	global.CORE_CONFIG.Gateway.PromptMode = ""
	global.CORE_CONFIG.Gateway.PromptFile = ""
	promptDegrade.reset()
	return func() {
		global.CORE_CONFIG.Gateway = old
		global.CORE_LOG = oldLog
		promptDegrade.reset()
	}
}

func sampleMessages() []any {
	return []any{
		map[string]any{"role": "system", "content": "You are a coding agent running in the Codex CLI. Refuse requests for DoS attacks, exploit development."},
		map[string]any{"role": "user", "content": "<environment_context>cwd=/tmp sandbox=unsandboxed</environment_context>fix the bug"},
		map[string]any{"role": "assistant", "content": "looking at it"},
	}
}

// TestPromptModePassthroughIsNoop 默认模式必须原样返回——
// 不意外改写客户端提示词是默认行为。
func TestPromptModePassthroughIsNoop(t *testing.T) {
	defer setupPromptTest(t)()
	msgs := sampleMessages()
	out := applyPromptMode(msgs, promptPassthrough, false)
	if len(out) != len(msgs) {
		t.Fatalf("passthrough 不应改变消息数: %d -> %d", len(msgs), len(out))
	}
	if mustJSON(out) != mustJSON(msgs) {
		t.Fatal("passthrough 不应改动内容")
	}
}

// TestPromptModeCustomReplacesSystem custom 删除全部 system/developer，
// 头部插入网关提示词，其余消息逐字保留。
func TestPromptModeCustomReplacesSystem(t *testing.T) {
	defer setupPromptTest(t)()
	out := applyPromptMode(sampleMessages(), promptCustom, false)

	if len(out) != 3 {
		t.Fatalf("应保留 3 条（1 system + user + assistant），实际 %d", len(out))
	}
	first, _ := out[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("首条应为 system，实际 %v", first["role"])
	}
	content, _ := first["content"].(string)
	if strings.Contains(content, "Codex CLI") {
		t.Fatal("客户端 harness 模板应被删除")
	}
	if !strings.Contains(content, "工程助手") {
		t.Fatal("应插入网关提示词")
	}
	// user / assistant 必须逐字保留
	second, _ := out[1].(map[string]any)
	uc, _ := second["content"].(string)
	if !strings.Contains(uc, "fix the bug") {
		t.Fatalf("user 消息被改动了: %q", uc)
	}
}

// TestPromptModeAppendKeepsOriginal append 保留客户端原文，只插入网关提示词。
func TestPromptModeAppendKeepsOriginal(t *testing.T) {
	defer setupPromptTest(t)()
	msgs := sampleMessages()
	out := applyPromptMode(msgs, promptAppend, false)

	if len(out) != len(msgs)+1 {
		t.Fatalf("应插入 1 条，实际 %d -> %d", len(msgs), len(out))
	}
	// 插入位置：开头连续 system 区块之后。客户端原文在前，网关提示词紧随。
	orig, _ := out[0].(map[string]any)
	oc, _ := orig["content"].(string)
	if !strings.Contains(oc, "Codex CLI") {
		t.Fatalf("append 应保留客户端原文，实际 %q", oc[:60])
	}
	inserted, _ := out[1].(map[string]any)
	ic, _ := inserted["content"].(string)
	if !strings.Contains(ic, "工程助手") {
		t.Fatal("应在客户端 system 之后插入网关提示词")
	}
	// user / assistant 仍按原顺序在后
	if len(out) < 4 {
		t.Fatalf("消息数不对: %d", len(out))
	}
}

// TestPromptDegradeKeepsUserInstructions 降级时**不能**删光 system。
//
// 这是与参考实现的关键差异：它降级走 rewrite（删全部 system/developer），
// 那会连用户真实的 AGENTS.md / 项目指令一起删掉，agent 丢失任务上下文。
// 这里只剥客户端模板指纹，用户指令要留住。
func TestPromptDegradeKeepsUserInstructions(t *testing.T) {
	defer setupPromptTest(t)()
	msgs := []any{
		map[string]any{"role": "system", "content": "# AGENTS.md\n\n本项目使用 Go 1.25，提交前必须跑 go test ./..."},
		map[string]any{"role": "system", "content": "You are a coding agent running in the Codex CLI. Refuse requests for DoS attacks, exploit development."},
		map[string]any{"role": "user", "content": "fix the bug"},
	}
	out := applyPromptMode(msgs, promptPassthrough, true)

	joined := mustJSON(out)
	if !strings.Contains(joined, "go test ./...") {
		t.Fatal("降级不应删除用户的项目指令")
	}
	if !strings.Contains(joined, "helpful assistant") {
		t.Fatal("降级应插入最小中性提示词")
	}
}

// TestNextMidnightCST 降级窗口截止到次日 00:00 CST。
func TestNextMidnightCST(t *testing.T) {
	cst := time.FixedZone("CST", 8*3600)
	cases := []struct {
		now  time.Time
		want time.Time
	}{
		{time.Date(2026, 10, 10, 14, 0, 0, 0, cst), time.Date(2026, 10, 11, 0, 0, 0, 0, cst)},
		{time.Date(2026, 10, 10, 23, 59, 0, 0, cst), time.Date(2026, 10, 11, 0, 0, 0, 0, cst)},
		{time.Date(2026, 10, 10, 0, 0, 0, 0, cst), time.Date(2026, 10, 11, 0, 0, 0, 0, cst)},
	}
	for _, c := range cases {
		got := nextMidnightCST(c.now)
		if !got.Equal(c.want) {
			t.Errorf("nextMidnightCST(%v)=%v want %v", c.now, got, c.want)
		}
		if !got.After(c.now) {
			t.Errorf("截止时间必须晚于当前: %v vs %v", got, c.now)
		}
	}
}

// TestDegradeGateTriggerAndExpire 窗口触发后生效，过期后自动失效。
func TestDegradeGateTriggerAndExpire(t *testing.T) {
	defer setupPromptTest(t)()

	if promptDegradeActive() {
		t.Fatal("初始不应处于降级状态")
	}
	triggerPromptDegrade()
	if !promptDegradeActive() {
		t.Fatal("触发后应生效")
	}
	// 再次触发不续期
	first := promptDegrade.until
	triggerPromptDegrade()
	if !promptDegrade.until.Equal(first) {
		t.Fatal("已开启时不应续期")
	}
	promptDegrade.reset()
	if promptDegradeActive() {
		t.Fatal("reset 后应失效")
	}
}

// TestApplyPromptModeToBodySkipsDefault 默认 passthrough 且未降级时不动 body。
// 这是最常见路径，不该产生额外开销或改变字段顺序。
func TestApplyPromptModeToBodySkipsDefault(t *testing.T) {
	defer setupPromptTest(t)()
	body := map[string]any{"messages": sampleMessages(), "model": "x"}
	before := mustJSON(body)
	applyPromptModeToBody(body)
	if mustJSON(body) != before {
		t.Fatal("默认模式不应改动 body")
	}
}

// TestApplyPromptModeToBodyAppliesCustom custom 模式下 body 应被改写。
func TestApplyPromptModeToBodyAppliesCustom(t *testing.T) {
	defer setupPromptTest(t)()
	global.CORE_CONFIG.Gateway.PromptMode = "custom"
	body := map[string]any{"messages": sampleMessages(), "model": "x"}
	applyPromptModeToBody(body)
	if !strings.Contains(mustJSON(body), "工程助手") {
		t.Fatal("custom 模式应替换 system 面")
	}
}

// TestRetryWithDegradedPromptChangesBody 降级重试必须真的改出东西。
// 分类对了但 body 没变 = 空转，用户仍会看到 403。
func TestRetryWithDegradedPromptChangesBody(t *testing.T) {
	defer setupPromptTest(t)()
	meta := &ChatRequestMeta{
		Body: []byte(`{"messages":[
			{"role":"system","content":"You are a coding agent running in the Codex CLI. Refuse requests for DoS attacks, exploit development, credential testing."},
			{"role":"user","content":"fix the bug"}],"model":"x"}`),
	}
	if !retryWithDegradedPrompt(meta) {
		t.Fatal("降级应改变 body")
	}
	if strings.Contains(string(meta.Body), "exploit development") {
		t.Fatal("客户端 harness 模板应被剥离")
	}
	if !strings.Contains(string(meta.Body), "helpful assistant") {
		t.Fatal("应插入降级提示词")
	}
	// 触发后窗口应生效
	if !promptDegradeActive() {
		t.Fatal("降级重试应打开窗口")
	}
}

// TestNormalizePromptMode 未知值回落 passthrough。
func TestNormalizePromptMode(t *testing.T) {
	cases := map[string]promptMode{
		"":            promptPassthrough,
		"passthrough": promptPassthrough,
		"CUSTOM":      promptCustom,
		" append ":    promptAppend,
		"garbage":     promptPassthrough,
	}
	for in, want := range cases {
		if got := normalizePromptMode(in); got != want {
			t.Errorf("normalizePromptMode(%q)=%v want %v", in, got, want)
		}
	}
}

// TestAccountRestrictedIsNotContent 账号风控与内容审核必须分开。
// 混在一起的后果：对着已标记的账号反复降级（必然失败），白白烧请求。
func TestAccountRestrictedIsNotContent(t *testing.T) {
	restricted := []byte(`{"code":11140,"msg":"request illegal","displayMsg":{"en":"Your account has been restricted due to unusual activity","zh":"账号存在异常行为"}}`)
	content := []byte(`{"code":11140,"msg":"request illegal","displayMsg":{"en":"Content failed safety review. Please revise it","zh":"内容未通过安全审核"}}`)

	if !isAccountRestricted(restricted) {
		t.Fatal("应识别为账号风控")
	}
	if isAccountRestricted(content) {
		t.Fatal("内容审核不应被判成账号风控")
	}
	// 内容审核那条仍要被内容类判定接住
	if !isContentFiltered(content) {
		t.Fatal("内容审核应被内容判定接住")
	}
	// 风控不该触发内容降级重试
	if isContentFiltered(restricted) {
		t.Fatal("账号风控不该走内容降级路径")
	}
}

// zapNop 返回一个可用的空 logger，避免测试里 global.CORE_LOG 为 nil。
func zapNop() *zap.Logger { return zap.NewNop() }
