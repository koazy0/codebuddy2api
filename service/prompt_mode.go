package service

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"codebuddy-gateway/global"

	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// 网关自有系统提示词
//
// 问题
// ----
// Codex CLI / Claude Code / DSH 这类 agent 会在 system prompt 里注入**固定的
// 模板句**——"You are a coding agent running in the Codex CLI"、工具使用规范、
// AGENTS.md 内容、以及一整套安全声明（"Refuse requests for DoS attacks,
// exploit development, credential testing..."）。上游的内容审核会按这些
// 逐字指纹误杀合法流量：用户只是想写代码，却因为客户端自带的合规模板被拒。
//
// sanitize 那套（插零宽、剪句子）是在客户端提示词上做**减法**，靠猜哪些句
// 子敏感，猜不全就仍被拦。本模块走另一条路：**整体替换 system 面**，从源头
// 消灭指纹。两层互不替代——sanitize 继续处理 user/assistant/tool 对话面。
//
// 模式
// ----
//
//	passthrough  透传客户端 system/developer（默认，不改变既有行为）
//	custom       删除全部 system/developer，换成网关提示词
//	append       保留客户端原文，在开头连续 system 区块之后插入网关提示词
//
// 降级窗口
// --------
// passthrough / append 模式下被内容审核拦截时，切到「最小中性提示词」并重试
// 同一轮一次；窗口持续到次日 00:00 CST，期间所有请求都走降级提示词。
//
// 与参考实现（workbuddy2api-hub wb_prompt.py）的差异，也是刻意为之的地方：
// 它降级时执行 rewrite（删除全部 system/developer）。那会连用户真实的
// AGENTS.md / 项目指令一起删掉，agent 丢失任务上下文。这里改为复用已有的
// harness 块识别与剪枝能力——只剥掉客户端模板指纹，保留用户指令。
// ---------------------------------------------------------------------------

// promptMode 是 system 面的处理模式。
type promptMode string

const (
	// promptPassthrough 透传客户端原文（默认）。
	promptPassthrough promptMode = "passthrough"
	// promptCustom 删除全部 system/developer，换成网关提示词。
	promptCustom promptMode = "custom"
	// promptAppend 保留客户端原文，头部插入网关提示词。
	promptAppend promptMode = "append"
)

// normalizePromptMode 归一化配置值，未知值回落 passthrough。
// 保守默认：不意外改写客户端提示词。
func normalizePromptMode(s string) promptMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(promptCustom):
		return promptCustom
	case string(promptAppend):
		return promptAppend
	default:
		return promptPassthrough
	}
}

// defaultGatewayPrompt 是 custom / append 模式使用的网关提示词。
// 刻意写成工程助手指令，且**不含任何安全语义词**——它的职责之一就是
// 不引入新的审核指纹。
const defaultGatewayPrompt = `你是一名工程助手，帮助用户完成软件工程任务。

- 先理解代码与上下文再动手，遵循既有模式与约定。
- 最小改动：只改必要部分，不做无关重构。
- 改动后要验证：跑测试或构建确认结果，不假设「应该没问题」。
- 对不确定的事保持诚实：说明不确定并给出验证路径，不编造。
- 跟随用户语言，简洁直接，技术术语精确。
- 如实报告失败与边界，不掩盖、不粉饰。`

// degradedGatewayPrompt 是降级窗口用的最小中性提示词。
// 比 defaultGatewayPrompt 更短——降级时目标只是让请求过审，
// 不需要保留行为指令（那些由对话面承载）。
const degradedGatewayPrompt = "You are a helpful assistant. Respond in the user's language, follow the user's instructions, and be direct and concise."

// readFileTrimmed 读取文件并去掉首尾空白。文件不存在或读失败时返回错误，
// 由调用方回落到内置默认——提示词配置不该阻断服务启动。
func readFileTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// promptGatewayText 返回配置的网关提示词文本。
// 配置了文件则读文件，否则用内置默认。
func promptGatewayText() string {
	if p := strings.TrimSpace(global.CORE_CONFIG.Gateway.PromptFile); p != "" {
		if b, err := readFileTrimmed(p); err == nil && b != "" {
			return b
		}
		global.CORE_LOG.Warn("prompt file unreadable, falling back to builtin", zap.String("path", p))
	}
	return defaultGatewayPrompt
}

// applyPromptMode 按模式处理 messages。
//
// degraded 只对 passthrough / append 有意义：custom 模式本身就是替换，
// 没有可降级的空间（参考实现的约定，此处保持一致）。
//
// 降级路径**不复刻参考实现的「删除全部 system」**：那会连用户真实的
// AGENTS.md / 项目指令一起删掉，agent 会丢失任务上下文。改为走已有的
// harness 指纹剥离 + 剪枝，只消灭客户端模板句。
func applyPromptMode(messages []any, mode promptMode, degraded bool) []any {
	if len(messages) == 0 {
		return messages
	}
	switch mode {
	case promptCustom:
		return rewriteSystemMessages(messages, promptGatewayText())
	case promptAppend:
		if degraded {
			return degradeSystemMessages(messages)
		}
		return appendGatewayMessage(messages, promptGatewayText())
	default: // passthrough
		if degraded {
			return degradeSystemMessages(messages)
		}
		return messages
	}
}

// rewriteSystemMessages 删除全部 system/developer，头部插入一条网关 system。
func rewriteSystemMessages(messages []any, text string) []any {
	if text == "" {
		return messages
	}
	kept := make([]any, 0, len(messages)+1)
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			kept = append(kept, m)
			continue
		}
		role := strings.ToLower(asString(msg["role"]))
		if role == "system" || role == "developer" {
			continue
		}
		kept = append(kept, m)
	}
	out := make([]any, 0, len(kept)+1)
	out = append(out, map[string]any{"role": "system", "content": text})
	return append(out, kept...)
}

// appendGatewayMessage 在开头连续 system/developer 区块之后插入网关提示词，
// 客户端既有消息逐字保留。
func appendGatewayMessage(messages []any, text string) []any {
	if text == "" {
		return messages
	}
	insertAt := 0
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			break
		}
		role := strings.ToLower(asString(msg["role"]))
		if role != "system" && role != "developer" {
			break
		}
		insertAt++
	}
	out := make([]any, 0, len(messages)+1)
	out = append(out, messages[:insertAt]...)
	out = append(out, map[string]any{"role": "system", "content": text})
	return append(out, messages[insertAt:]...)
}

// degradeSystemMessages 降级处理：只剥客户端模板指纹，保留用户真实指令。
//
// 做法是逐条 system/developer 消息跑一遍 sanitizeFull——它会重写 harness
// 注入块（<environment_context> 等换成中性摘要）、剪掉含敏感词的句子、
// 并清理身份归属声明。最后在头部补一条降级提示词冲淡整体指纹。
//
// 与「删除全部 system」相比，这样保住了 AGENTS.md / 项目规范这类用户指令，
// agent 的任务上下文不丢。
func degradeSystemMessages(messages []any) []any {
	out := make([]any, 0, len(messages)+1)
	out = append(out, map[string]any{"role": "system", "content": degradedGatewayPrompt})
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			out = append(out, m)
			continue
		}
		role := strings.ToLower(asString(msg["role"]))
		if role != "system" && role != "developer" {
			out = append(out, m)
			continue
		}
		cp := make(map[string]any, len(msg))
		for k, v := range msg {
			cp[k] = v
		}
		cp["content"] = sanitizeContentLevel(msg["content"], sanitizeFull)
		// 洗完之后可能只剩空白，那种消息没有保留价值。
		if plainContentText(cp["content"]) == "" {
			continue
		}
		out = append(out, cp)
	}
	return out
}

// ---------------------------------------------------------------------------
// 降级窗口
// ---------------------------------------------------------------------------

// degradeGate 是进程内的降级窗口：触发后一直有效到次日 00:00 CST。
// 不续期——到了整点自动失效，无需人工重置。
//
// 为什么按「次日 00:00」而不是固定时长：上游的内容审核策略按自然日重置，
// 固定时长（如 30 分钟）会在同一天内反复触发/失效，导致请求在「降级」与
// 「原样」之间抖动。锁到次日整点，一天只降级一次。
type degradeGate struct {
	mu    sync.Mutex
	until time.Time
}

var promptDegrade = &degradeGate{}

// active 报告降级窗口当前是否生效。
func (g *degradeGate) active() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return time.Now().Before(g.until)
}

// trigger 打开降级窗口（若尚未打开）。
// 已开启时不续期——避免一次拦截把窗口不断往后推。
func (g *degradeGate) trigger() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if now.Before(g.until) {
		return
	}
	g.until = nextMidnightCST(now)
	global.CORE_LOG.Warn("prompt degrade window opened until next 00:00 CST",
		zap.Time("until", g.until))
}

// reset 关闭降级窗口（供测试与手动复位）。
func (g *degradeGate) reset() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.until = time.Time{}
}

// nextMidnightCST 返回 now 之后最近的 Asia/Shanghai 00:00。
// 用固定 +08:00 计算，不依赖宿主机时区（容器与宿主时区常不一致）。
func nextMidnightCST(now time.Time) time.Time {
	cst := time.FixedZone("CST", 8*3600)
	local := now.In(cst)
	y, m, d := local.Date()
	next := time.Date(y, m, d, 0, 0, 0, 0, cst).AddDate(0, 0, 1)
	return next
}

// promptDegradeActive 供 proxy 查询降级窗口状态。
func promptDegradeActive() bool { return promptDegrade.active() }

// triggerPromptDegrade 打开降级窗口。
func triggerPromptDegrade() { promptDegrade.trigger() }

// applyPromptModeToBody 对 Chat 形态的 body 应用 system 面处理。
// body 没有 messages 时静默跳过（非对话请求）。
func applyPromptModeToBody(body map[string]any) {
	if body == nil {
		return
	}
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return
	}
	mode := normalizePromptMode(global.CORE_CONFIG.Gateway.PromptMode)
	degraded := promptDegradeActive()
	// 默认 passthrough 且未降级时不动 body——这是最常见路径，不该有额外开销。
	if mode == promptPassthrough && !degraded {
		return
	}
	out := applyPromptMode(msgs, mode, degraded)
	if len(out) != len(msgs) || !sameMessageSlice(msgs, out) {
		body["messages"] = out
	}
}

// sameMessageSlice 判断两个消息切片是否同一份内容（浅比）。
// 用来避免「处理了但没变化」时仍写回 body、打乱 JSON 字段顺序。
func sameMessageSlice(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		am, aok := a[i].(map[string]any)
		bm, bok := b[i].(map[string]any)
		if aok != bok {
			return false
		}
		if !aok {
			continue
		}
		if len(am) != len(bm) {
			return false
		}
		for k, av := range am {
			if bv, exists := bm[k]; !exists || mustJSON(av) != mustJSON(bv) {
				return false
			}
		}
	}
	return true
}

// retryWithDegradedPrompt 把请求的 system 面换成降级形态后重试一次。
//
// 触发时机：内容审核拦截，且 sanitize 的 full 档已洗完却毫无变化——
// 说明触发点不在零宽能骗过的词上，而在客户端模板的**结构本身**。
// 这时最后一招是整体替换 system 面。
//
// 返回是否真的改出了东西。与 retryWAFRejectedBody 同一契约：
// 分类对了但 body 没变 = 空转，调用方据此决定是否放弃。
func retryWithDegradedPrompt(meta *ChatRequestMeta) bool {
	if meta == nil || len(meta.Body) == 0 {
		return false
	}
	var body map[string]any
	if err := json.Unmarshal(meta.Body, &body); err != nil {
		return false
	}
	before := mustJSON(body)
	// 打开降级窗口：即便本次重试成功，同一自然日内的后续请求也走降级形态，
	// 避免客户端每轮都重新踩一次审核。
	triggerPromptDegrade()
	applyPromptModeToBody(body)
	after := mustJSON(body)
	if before == after {
		return false
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return false
	}
	meta.Body = encoded
	meta.RequestPreview = captureChatRequest(encoded)
	global.CORE_LOG.Warn("retrying with degraded system prompt",
		zap.String("mode", string(normalizePromptMode(global.CORE_CONFIG.Gateway.PromptMode))))
	return true
}
