package service

import (
	"strings"
	"testing"
)

// TestProbeRealisticCodexPrompt 用一段仿真的 Codex 系统提示跑一遍，
// 人工确认清洗后仍然保留身份与行为准则（配合 -v 查看输出）。
func TestProbeRealisticCodexPrompt(t *testing.T) {
	prompt := strings.Join([]string{
		"You are a coding agent running in the Codex CLI, a terminal-based coding assistant. Codex CLI is an open source project led by OpenAI. You are expected to be precise, safe, and helpful.",
		"",
		"Within this context, Codex refers to the open-source agentic coding interface (not the old Codex language model built by OpenAI).",
		"",
		"## Personality",
		"Your default personality and tone is concise, direct, and friendly.",
		"",
		"# AGENTS.md spec",
		"- Repos often contain AGENTS.md files.",
		"",
		"## Sandbox and approvals",
		"Codex may run with escalated privileges. Do not perform destructive actions without approval. Never attempt to weaponize a vulnerability.",
	}, "\n")

	out := sanitizeSystemText(prompt)
	plain := stripZeroWidth(out)

	t.Logf("=== sanitized (zero-width stripped) ===\n%s", plain)

	for _, keep := range []string{"Codex CLI", "## Personality", "# AGENTS.md spec", "concise, direct, and friendly"} {
		if !strings.Contains(plain, keep) {
			t.Fatalf("lost section: %q", keep)
		}
	}
	for _, gone := range []string{"open source project led by", "built by OpenAI"} {
		if strings.Contains(strings.ToLower(plain), gone) {
			t.Fatalf("attribution survived: %q", gone)
		}
	}
	// 裸的敏感词不能再出现（零宽化之后不算裸）。
	for _, raw := range []string{"OpenAI", "sandbox", "weaponize", "vulnerability"} {
		if strings.Contains(out, raw) {
			t.Fatalf("raw term %q survived", raw)
		}
	}
}
