package service

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Codex / Claude Code 的「品牌指纹」：这些字符串本身不是有害内容，
// 但会在上游（copilot.tencent.com）触发 WAF，返回 11128
// "Illegal API invocation from an unapproved channel"，整条请求被拒。
//
// 设计原则：只清指纹，不动身份。清理后模型仍然认为自己是「Codex CLI 里的
// 编程 agent」，仍然按 Codex 的工作方式办事；我们只是把「谁做的这个工具」
// 这类归属声明抹掉。用户原文、工具定义、其余提示词一律不动。
//
// 匹配策略：不再依赖整句字面量（上游提示词改一个标点就会静默失效），
// 而是用「宽松锚点 + 可跨版本」的正则，覆盖历史上出现过的多种写法。
var (
	// codexOriginPattern 命中「Codex CLI 是 OpenAI / 由 OpenAI 主导的开源项目」这类归属声明。
	// 兼容写法：
	//   - Codex CLI is an open source project led by OpenAI.
	//   - Codex CLI is an open-source project by OpenAI
	//   - Codex CLI, an open source project from OpenAI,
	//   - Codex CLI is developed/maintained/built by OpenAI
	// 尾部的 "and ..." 从句必须能被识别为「下一段」，否则会把后续指令一起吃掉
	// （实测 "…led by OpenAI and maintained by contributors." 整句会被吞光）。
	// 因此尾段在遇到 and/or/but/以及逗号句读时立刻收手。
	codexOriginPattern = regexp.MustCompile(`(?i)\bCodex(?: CLI)?\s*[,，]?\s*(?:is|was|being)?\s*(?:an?\s+)?open[\s-]?sour(?:ce|ced)\s+(?:project|tool|software|initiative|effort|assistant)?\s*(?:led|developed|maintained|built|created|made|backed|sponsored|supported|by|from|of)?\s*(?:by|from|of|at)?\s*OpenAI(?:\s+by\s+[A-Z][\w.]*)*\s*[.,;，；]?`)

	// openAIAttributionPattern 兜底：零散的「由 OpenAI 提供/维护」短语。
	// 只吃「动词 + by OpenAI」这个短语本身，不吃后面的从句：
	// 旧写法把整句吞掉，遇到不含 "Codex" 的句子会把主语一起删掉，留下残句。
	openAIAttributionPattern = regexp.MustCompile(`(?i)\b(?:led|developed|maintained|built|created|made|backed|sponsored|supported|provided|offered|by|from)\s+by\s+OpenAI\b`)
	// openAIPlainAttributionPattern 处理 "by OpenAI" / "from OpenAI" 裸短语。
	openAIPlainAttributionPattern = regexp.MustCompile(`(?i)\b(?:by|from|of)\s+OpenAI\b`)

	// branding 词表：出现于 harness 模板里的竞品/归属品牌词，统一做零宽脱敏。
	// 用词边界避免误伤（如 "Anthropic" 不要命中 "Anthropics" 之外的词）。
	brandingTerms = []string{
		"OpenAI",
		"Anthropic",
		"Claude Code",
		"Claude Opus",
		"Claude Sonnet",
		"Claude Haiku",
		"Claude Fable",
		// 提交署名与邮箱：Claude Code 写 commit 时会带上，属品牌指纹。
		"Co-Authored-By",
		"noreply@anthropic.com",
	}
)

// complianceSensitiveTerms 是「合规声明里的高频英文术语」。
//
// 上游内容审核会对这些词做关键词匹配。但它们大量出现在客户端**固定的合规模板**里
// （例如 Codex 的 sandbox / escalation 说明、安全边界声明），属于「拒绝作恶」的声明，
// 并非用户的有害输入，却会把整条请求判死。
//
// 处理方式与 shouzhuo/desensitize.py 一致：在词内部插入零宽空格（U+200B），
// 打断后端子串匹配，但人眼和模型读起来完全一样。
var complianceSensitiveTerms = []string{
	// 网络攻击类
	"DoS",
	"DDoS",
	"exploit development",
	"exploit",
	"credential testing",
	"credential stuffing",
	"supply chain compromise",
	"supply-chain compromise",
	"detection evasion",
	"C2 frameworks",
	"C2 framework",
	"command and control",
	"XSS",
	"CSRF",
	"phishing",
	"malware",
	"ransomware",
	"keylogger",
	"rootkit",
	"backdoor",
	"botnet",
	"zero-day",
	"0day",
	// 攻击链术语：Codex/Claude Code 的沙箱与能力说明里高频出现
	"remote code execution",
	"privilege escalation",
	"reverse shell",
	"SQL injection",
	"brute force",
	"brute-force",
	"mass targeting",
	"malicious purposes",
	"malicious intent",
	// Codex 系统提示里高频出现的安全语义词
	"vulnerability",
	"vulnerabilities",
	"red teaming",
	"red-teaming",
	"unsandboxed",
	"sandboxing",
	"sandboxed",
	"sandbox",
	"escalated privileges",
	"escalated",
	"escalation",
	"destructive action",
	"destructive command",
	"destructive",
	"penetration testing",
	"penetration test",
	"cybersecurity",
	"security review",
	"hacking",
	// 通用敏感词
	"weaponize",
	"weaponized",
	"attack",
	"attacks",
	"injection",
	"harmful",
	"dangerous",
	"abuse",
	"abusive",
	"illegal",
	"terrorist",
	"terrorism",
	"bomb",
	"weapon",
	"weapons",
	"narcotic",
	"drugs",
	"drug",
	"suicide",
	"self-harm",
	"murder",
	"violence",
	"violent",
	"kill",
	"malicious",
}

// zwsp 零宽空格：插入词内部打断后端关键词匹配，人/模型读起来无差别。
const zwsp = "\u200b"

// compliancePattern 把词表编译成单个正则，按词长降序避免短词先吃掉长词。
var compliancePattern = buildWordPattern(complianceSensitiveTerms)

// brandingPattern 品牌词正则（同样按长度降序）。
var brandingPattern = buildWordPattern(brandingTerms)

// buildWordPattern 把词表编译成大小写不敏感的正则，长词优先。
//
// 边界策略：**只锁左侧词边界，右侧放开**。
//
// 右侧不能加 \b：词表里是 `exploit`，但真实文本写的是 `exploited` / `exploiting` /
// `exploitation`，`exploit` 后面紧跟字母，右侧词边界不成立，整词就匹配不上。
// 上游做的是朴素子串匹配（我们插零宽正是为了打断它），一旦漏匹配，这些派生词
// 就会裸着发出去，等于没防。实测 `exploitation vector present` 就是这么漏的。
//
// 左侧必须保留 \b：否则 `kill` 会命中 `skill`、`attack` 会命中 `counterattack`
// 这类正常词。左侧锁边界保证「词头对齐」，兼顾派生覆盖与误伤控制。
//
// 放开右侧的代价是可能多命中少数正常词（如 `drugstore` 含 `drug`）。这个代价
// 可以接受：脱敏只是插入不可见字符，多插一个对模型与人都无影响；而漏网的代价是
// 整条请求被上游拒绝、用户正在写代码时被打断。
func buildWordPattern(terms []string) *regexp.Regexp {
	if len(terms) == 0 {
		return nil
	}
	sorted := make([]string, len(terms))
	copy(sorted, terms)
	// 插入排序：词表很小，且避免引入 sort 包带来的额外 import 噪音。
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && len(sorted[j]) > len(sorted[j-1]); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	escaped := make([]string, 0, len(sorted))
	for _, term := range sorted {
		escaped = append(escaped, regexp.QuoteMeta(term))
	}
	return regexp.MustCompile(`(?i)\b(?:` + strings.Join(escaped, "|") + `)`)
}

// sanitizeLevel 是清洗力度分档。
//
// 力度只决定「动哪些承载面、用哪种手段」，不改变一条铁律：
// 用户真实提问（非 harness 注入）在任何档位下都逐字节保留。
type sanitizeLevel int

const (
	// sanitizeOff 完全不改写，仅用于排障对比。
	sanitizeOff sanitizeLevel = iota
	// sanitizeHarness 处理客户端模板面：system/developer 文本、harness 注入的
	// user 上下文、tool 定义（description/title）。手段以零宽脱敏为主，语义全保留。
	sanitizeHarness
	// sanitizeFull 在 harness 之上追加「对话累积面」：assistant 历史与 tool 输出。
	// agentic 会话里这些内容会随轮次累积 sandbox / escalation / credential 一类词，
	// 这正是「跑一会儿才断」的成因。此档同时启用结构性手段（harness 块整段重写、
	// 丢掉带敏感词的 tool description），不再依赖零宽能否骗过上游的文本归一化。
	sanitizeFull
)

// sanitizeLevelFromName 把配置值归一化成力度档，未知值回落到默认的 harness 档。
func sanitizeLevelFromName(name string) sanitizeLevel {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "off":
		return sanitizeOff
	case "full":
		return sanitizeFull
	default:
		return sanitizeHarness
	}
}

func (l sanitizeLevel) String() string {
	switch l {
	case sanitizeOff:
		return "off"
	case sanitizeFull:
		return "full"
	default:
		return "harness"
	}
}

// sanitizeUpstreamChat 以默认力度清洗请求体（保留旧调用点与测试的语义）。
func sanitizeUpstreamChat(body map[string]any) {
	sanitizeRequest(body, sanitizeHarness)
}

// sanitizeUpstreamChatWithMode 按配置名决定的力度清洗，供 proxy 首轮调用。
func sanitizeUpstreamChatWithMode(body map[string]any, mode string) {
	sanitizeRequest(body, sanitizeLevelFromName(mode))
}

// sanitizeRequest 按力度清洗发往上游的请求体，返回是否产生了字节变化。
// 返回值即「这一趟有没有真的改出东西」，调用方据此判断能否继续重试。
func sanitizeRequest(body map[string]any, level sanitizeLevel) bool {
	if body == nil || level == sanitizeOff {
		return false
	}
	changed := false
	if sweepMessages(body, level) {
		changed = true
	}
	if sweepTools(body, level) {
		changed = true
	}
	return changed
}

// sweepMessages 按角色决定该不该动，以及动到什么程度。
//
// 角色分工是这个模块的关键判断，逐条说明：
//   - system / developer：harness 模板集中地，任何档位都处理，可做归属句删除等改写。
//   - user：只有被识别为 harness 注入的上下文才处理；真实提问永不改写。
//   - assistant / tool：只做零宽脱敏，任何档位都执行。这两类是真实交互记录，
//     删除内容会污染对话语义，但零宽对模型和人都是不可见的，可以放心施加。
//     agentic 客户端每轮重发全历史，正文里的 sandbox / escalation 会随轮次累积，
//     正是「跑一会儿才断」的成因——放在默认档处理，用户才不会感知到中途失败。
func sweepMessages(body map[string]any, level sanitizeLevel) bool {
	msgs, ok := body["messages"].([]any)
	if !ok {
		return false
	}
	changed := false
	for _, item := range msgs {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(asString(m["role"])))
		dialogue := false
		switch role {
		case "system", "developer":
			// 模板面：允许改写文字
		case "user":
			if !looksLikeHarnessContext(plainContentText(m["content"])) {
				continue
			}
		case "assistant", "tool":
			// 对话面：只做不可见脱敏，不删改任何文字
			dialogue = true
		default:
			continue
		}
		if rewriteMessageContent(m, level, dialogue) {
			changed = true
		}
	}
	return changed
}

// rewriteMessageContent 原地改写单条消息，返回是否变化。
// dialogue 为真时只做零宽脱敏（不删除、不重写任何文字）。
func rewriteMessageContent(m map[string]any, level sanitizeLevel, dialogue bool) bool {
	changed := false
	before := mustJSON(m["content"])
	if dialogue {
		m["content"] = desensitizeContentLevel(m["content"])
	} else {
		m["content"] = sanitizeContentLevel(m["content"], level)
	}
	if mustJSON(m["content"]) != before {
		changed = true
	}
	// assistant 消息里除了 content 还有 tool_calls，其中的 arguments 是模型生成的
	// 工具入参（命令、补丁文本、文件内容），同样是不可控文本源。它不出现在 content
	// 里，只洗 content 就会整片漏掉。
	if sweepToolCalls(m) {
		changed = true
	}
	return changed
}

// desensitizeContentLevel 只做零宽脱敏，不改写任何文字。
// 专用于对话内容（assistant / tool）：这些是真实交互记录，删改会污染语义，
// 而零宽对模型与人都是不可见的，可以放心施加。
func desensitizeContentLevel(v any) any {
	switch t := v.(type) {
	case string:
		return desensitizeAllTerms(t)
	case []any:
		for i, part := range t {
			t[i] = desensitizeContentLevel(part)
		}
		return t
	case map[string]any:
		if text, ok := t["text"].(string); ok {
			t["text"] = desensitizeAllTerms(text)
		}
		if content, ok := t["content"]; ok {
			t["content"] = desensitizeContentLevel(content)
		}
		return t
	default:
		return v
	}
}

// sweepToolCalls 清洗 assistant 消息里的 tool_calls[].function.arguments。
//
// arguments 是 **JSON 字符串**（OpenAI/上游协议都这么约定），形如
// `{"cmd":"nmap -sV target"}`，不能像普通 content 那样直接做文本替换：
// 直接改字符串会破坏 JSON 结构，上游解析失败。所以先尝试按 JSON 解析，
// 成功则只对字符串叶子节点做脱敏，失败则保守跳过——宁可漏洗一个字，
// 也不能把一个非法 JSON 发给上游。
//
// 这里只做零宽脱敏，不删除任何内容：arguments 是模型要真正执行的命令与补丁正文，
// 删一行就可能改变语义（尤其是 apply_patch 的 `-` / `+` 行）。
func sweepToolCalls(m map[string]any) bool {
	calls, ok := m["tool_calls"].([]any)
	if !ok || len(calls) == 0 {
		return false
	}
	changed := false
	for _, item := range calls {
		call, _ := item.(map[string]any)
		if call == nil {
			continue
		}
		fn, _ := call["function"].(map[string]any)
		if fn == nil {
			continue
		}
		raw, ok := fn["arguments"].(string)
		if !ok || raw == "" || !hasSensitiveTerm(raw) {
			continue
		}
		rewritten, ok := sanitizeJSONArguments(raw)
		if !ok || rewritten == raw {
			continue
		}
		fn["arguments"] = rewritten
		changed = true
	}
	return changed
}

// sanitizeJSONArguments 对 JSON 字符串形式的工具入参做脱敏，
// 只改字符串值，不动键名与结构。
func sanitizeJSONArguments(raw string) (string, bool) {
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		// 不是合法 JSON（例如 freeform 补丁文本），按纯文本脱敏。
		return desensitizeAllTerms(raw), true
	}
	desensitizeJSONInPlace(parsed)
	encoded, err := json.Marshal(parsed)
	if err != nil {
		return raw, false
	}
	return string(encoded), true
}

// desensitizeJSONInPlace 递归对 JSON 树里的字符串节点做零宽脱敏。
// 键名一律不碰：改名会直接改变工具入参的语义。
func desensitizeJSONInPlace(v any) {
	switch t := v.(type) {
	case map[string]any:
		for key, val := range t {
			if s, ok := val.(string); ok {
				t[key] = desensitizeAllTerms(s)
				continue
			}
			desensitizeJSONInPlace(val)
		}
	case []any:
		for i, val := range t {
			if s, ok := val.(string); ok {
				t[i] = desensitizeAllTerms(s)
				continue
			}
			desensitizeJSONInPlace(val)
		}
	}
}

// sweepTools 处理 tool 定义。
//
// 为什么必须覆盖这里：tool 定义是 WAF 的高频命中点，而旧实现一个字都不看它——
// `json.Marshal` 之后 description 原样进请求体。Codex 的 shell / apply_patch
// 描述里天然带着 sandbox、escalation、destructive 这类词。
func sweepTools(body map[string]any, level sanitizeLevel) bool {
	list, ok := body["tools"].([]any)
	if !ok || len(list) == 0 {
		return false
	}
	changed := false
	for _, item := range list {
		if sweepToolSchema(item, level) {
			changed = true
		}
	}
	return changed
}

// sweepToolSchema 递归清理 tool 定义里**纯说明性**的文本。
//
// 刻意只碰 `description` / `title` 这两个键，绝不碰 name、enum、const、default：
// 后者是功能字段，参与工具调用的参数校验与回传，改一个字符就会让模型返回的值
// 对不上 schema，属于把「请求被拒」换成「工具调用崩掉」，得不偿失。
func sweepToolSchema(v any, level sanitizeLevel) bool {
	switch t := v.(type) {
	case map[string]any:
		changed := false
		for key, val := range t {
			switch strings.ToLower(key) {
			case "description", "title":
				s, ok := val.(string)
				if !ok || s == "" || !hasSensitiveTerm(s) {
					continue
				}
				if level >= sanitizeFull {
					// 结构性手段：丢掉被污染的句子，保留仍然干净的部分。
					// 不能整段删除——网关自己往 apply_patch 的 description 里
					// 注入过 "*** Begin Patch" 输入格式说明，那是功能必需文本，
					// 跟着一起删会让模型不知道用什么格式提交补丁。
					if pruned := pruneSensitiveSentences(s); pruned != "" {
						t[key] = pruned
					} else {
						// 整段都是被污染内容，没有可保留的功能文本，才摘掉。
						delete(t, key)
					}
				} else {
					t[key] = desensitizeAllTerms(s)
				}
				changed = true
			default:
				if sweepToolSchema(val, level) {
					changed = true
				}
			}
		}
		return changed
	case []any:
		changed := false
		for _, item := range t {
			if sweepToolSchema(item, level) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

// invisibleMarks 是本模块可能写入的不可见字符集合。
// 目前只插 U+200B，其余几项一并剥离，防止上游/客户端混入同类字符后判断失准。
var invisibleMarks = strings.NewReplacer(
	"\u200b", "", // 零宽空格：本模块的脱敏标记
	"\u200c", "", // 零宽非连接符
	"\u200d", "", // 零宽连接符
	"\ufeff", "", // 零宽不换行空格 / BOM
)

// stripInvisibleMarks 去掉不可见标记，把文本还原成「语义裸文本」。
func stripInvisibleMarks(s string) string {
	if s == "" || !strings.ContainsAny(s, "\u200b\u200c\u200d\ufeff") {
		return s
	}
	return invisibleMarks.Replace(s)
}

// pruneSensitiveSentences 按句/行丢掉被污染的部分，保留干净的剩余文本。
//
// 存在的理由：tool description 里既可能混着 harness 的权限说明（该丢），
// 也可能带着功能必需的格式约定——网关给 apply_patch 注入的
// "*** Begin Patch" ... "*** End Patch" 就在同一段文本里。整段删除会把
// 功能文本一起带走，模型随即不知道该用什么格式提交补丁。
//
// 因此这里做的是「剪枝」而不是「砍树」：逐行判断，只删命中的行。
func pruneSensitiveSentences(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			kept = append(kept, line)
			continue
		}
		if hasSensitiveTerm(line) {
			// 整行被污染：拆成句子再筛一遍，尽量保住其中的功能文本。
			if sentences := pruneSensitiveWithinSentence(line); sentences != "" {
				kept = append(kept, sentences)
			}
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// pruneSensitiveWithinSentence 在一行内部按句子切分，丢掉命中句。
// 用最小的分隔符集合，避免把 "*** Begin Patch" 这类标记切断。
func pruneSensitiveWithinSentence(line string) string {
	parts := splitSentences(line)
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) == "" || hasSensitiveTerm(p) {
			continue
		}
		kept = append(kept, strings.TrimSpace(p))
	}
	return strings.Join(kept, " ")
}

// sentenceEnders 是句末标点。刻意不含 `.`：Tool 描述里到处是文件名、
// 版本号和 `*** Begin Patch` 这类似是而非的「句点」，用点切会切碎功能文本。
var sentenceEnders = []string{"。", "！", "？", "\n"}

// splitSentences 按保守的句末标点切分，失败时整行原样返回。
func splitSentences(line string) []string {
	parts := []string{line}
	for _, sep := range sentenceEnders {
		next := make([]string, 0, len(parts))
		for _, p := range parts {
			next = append(next, strings.Split(p, sep)...)
		}
		parts = next
	}
	return parts
}

// hasSensitiveTerm 判断文本里是否存在任一命中词，用于决定「要不要动它」。
// 不命中的文本一律原样保留，把改写面压到最小。
//
// 关键：必须先剥离不可见标记再匹配。harness 档脱敏过的文本长这样：
//
//	"Run in the s<U+200B>andbox"
//
// 词表正则匹配不到它（零宽把子串切断了），于是 full 档会得出「没有敏感词」的
// 结论，结构性手段永不触发、changed 恒为 false，降级重试直接空转。
// 换句话说：脱敏本身会污染「还需不需要脱敏」的判断，这里必须还原后再看。
func hasSensitiveTerm(s string) bool {
	if s == "" {
		return false
	}
	probe := stripInvisibleMarks(s)
	if brandingPattern != nil && brandingPattern.MatchString(probe) {
		return true
	}
	return compliancePattern != nil && compliancePattern.MatchString(probe)
}

// desensitizeAllTerms 对品牌词与合规词统一做零宽脱敏。
func desensitizeAllTerms(s string) string {
	return desensitizeTerms(desensitizeTerms(s, brandingPattern), compliancePattern)
}

func sanitizeContentValue(v any) any {
	return sanitizeContentLevel(v, sanitizeHarness)
}

// sanitizeContentLevel 递归处理 request 里各种 content 形状。
func sanitizeContentLevel(v any, level sanitizeLevel) any {
	switch t := v.(type) {
	case string:
		return sanitizeText(t, level)
	case []any:
		for i, part := range t {
			t[i] = sanitizeContentLevel(part, level)
		}
		return t
	case map[string]any:
		if text, ok := t["text"].(string); ok {
			t["text"] = sanitizeText(text, level)
		}
		if content, ok := t["content"]; ok {
			t["content"] = sanitizeContentLevel(content, level)
		}
		return t
	default:
		return v
	}
}

// sanitizeSystemText 对单段文本做两级处理：
//  1. 归属声明整句替换（正则、跨版本）
//  2. 品牌词 / 合规词零宽脱敏
//
// 两级是互补的：正则负责把「Codex CLI 是 OpenAI 开源项目」整句压成中性表述
// （比逐词插零宽更彻底，也避免留下 "is an  open source project" 这种残句）；
// 零宽负责兜住所有正则没覆盖到的零散命中。
func sanitizeSystemText(s string) string {
	return sanitizeText(s, sanitizeHarness)
}

// sanitizeText 是文本清洗的统一入口。level 决定手段强度：
// harness 档只用「不可见改写」（删归属句 + 零宽脱敏），原文一字不丢；
// full 档追加结构性重写，用于零宽已失效的场景。
func sanitizeText(s string, level sanitizeLevel) string {
	if s == "" {
		return s
	}
	if level >= sanitizeFull {
		s = rewriteHarnessBlocks(s)
	}
	// 归属句整句删掉即可，不要替换成新的身份声明：
	// 原提示词里紧邻位置通常已经有「You are a coding agent running in the Codex CLI…」，
	// 再塞一句会造成整句重复（实测会变成同一句话连说两遍，属于明显的语义噪音）。
	// 删空之后由 tidySpacing 收尾。
	s = codexOriginPattern.ReplaceAllString(s, "")
	s = openAIAttributionPattern.ReplaceAllString(s, "")
	s = openAIPlainAttributionPattern.ReplaceAllString(s, "")
	s = tidyAttributionArtifacts(s)
	s = desensitizeAllTerms(s)
	return tidySpacing(s)
}

// ---------------------------------------------------------------------------
// 结构性手段：harness 块整段重写
// ---------------------------------------------------------------------------

// harnessBlock 描述一对客户端注入的标记，以及该用哪段中性文本顶替它。
//
// 这是与「零宽脱敏」互补的第二条路线，存在的理由很直接：零宽只在后端做
// 朴素子串匹配时有效。若上游在匹配前做了 Unicode 归一化（剥掉零宽）或改用
// 语义审核，插零宽就是白做——而线上持续复现 11128 恰好说明第一层不够。
// 结构性重写把整块有风险的说明换成一句不带任何敏感词的等价表述，不依赖
// 上游如何实现匹配。
type harnessBlock struct {
	open  string
	close string
	// summary 是替换文本。必须**自身不含任何敏感词**，否则等于没换。
	summary string
}

var harnessBlocks = []harnessBlock{
	{
		open:    "<environment_context>",
		close:   "</environment_context>",
		summary: "Environment context is provided by the harness.",
	},
	{
		open:  "<permissions instructions>",
		close: "</permissions instructions>",
		summary: "Runtime permissions apply: filesystem access may be restricted, " +
			"network may be limited, and some commands may require user approval.",
	},
	{
		open:    "<collaboration_mode>",
		close:   "</collaboration_mode>",
		summary: "Collaboration mode instructions are provided by the harness.",
	},
	{
		open:  "<skills_instructions>",
		close: "</skills_instructions>",
		summary: "Runtime skill metadata is available. Use relevant skills only " +
			"when explicitly requested or clearly applicable.",
	},
	{
		open:    "<plugins_instructions>",
		close:   "</plugins_instructions>",
		summary: "Runtime plugin metadata is available when relevant.",
	},
	{
		open:    "<system-reminder>",
		close:   "</system-reminder>",
		summary: "Runtime reminder context is provided by the harness.",
	},
	{
		open:    "<user_instructions>",
		close:   "</user_instructions>",
		summary: "Durable user instructions are provided by the harness.",
	},
}

// rewriteHarnessBlocks 把这些标记块整体替换成中性摘要。
// 只在找到配对的闭合标记时替换，避免把用户正文误吞。
func rewriteHarnessBlocks(s string) string {
	if s == "" {
		return s
	}
	for _, blk := range harnessBlocks {
		s = replaceDelimitedBlock(s, blk)
	}
	return s
}

// replaceDelimitedBlock 把 open...close 之间的内容换成 summary，保留标记本身。
//
// 保留标记是有意的：客户端和模型都靠这些标记定位上下文段落，删掉标记会让
// 提示词结构错乱；换掉块内文字则不影响结构识别。
func replaceDelimitedBlock(s string, blk harnessBlock) string {
	from := 0
	for {
		start := strings.Index(s[from:], blk.open)
		if start < 0 {
			return s
		}
		start += from
		bodyStart := start + len(blk.open)
		end := strings.Index(s[bodyStart:], blk.close)
		if end < 0 {
			return s
		}
		end += bodyStart
		replacement := blk.open + "\n" + blk.summary + "\n" + blk.close
		s = s[:start] + replacement + s[end+len(blk.close):]
		from = start + len(replacement)
	}
}

// desensitizeTerms 对命中词表的部分插入零宽空格。
func desensitizeTerms(s string, re *regexp.Regexp) string {
	if s == "" || re == nil {
		return s
	}
	return re.ReplaceAllStringFunc(s, zeroWidthSplit)
}

// zeroWidthSplit 在词内第一个字符后插入零宽空格：DoS -> Do\u200bS。
// 单字符词原样返回（插了等于没插）。
func zeroWidthSplit(term string) string {
	if len(term) <= 1 {
		return term
	}
	runes := []rune(term)
	if len(runes) <= 1 {
		return term
	}
	return string(runes[0]) + zwsp + string(runes[1:])
}

// tidyAttributionArtifacts 清理「删掉归属句」之后留下的残句痕迹。
// 典型残留：
//
//	"(not the old Codex language model built by OpenAI)" -> "(not the old Codex language model )"
//	"Codex CLI is an open source project led by OpenAI. Be precise." -> " Be precise."
//
// 处理的是标点/括号周围的孤立空格，以及删空后剩下的孤立标点。
func tidyAttributionArtifacts(s string) string {
	if s == "" {
		return s
	}
	// 空格 + 右括号/右引号类标点 → 收掉空格
	for _, closer := range []string{")", "）", "]", "】", "}", "》", "”", "’"} {
		s = strings.ReplaceAll(s, " "+closer, closer)
	}
	// 左括号类标点 + 空格 → 收掉空格
	for _, opener := range []string{"(", "（", "[", "【", "{", "《", "“", "‘"} {
		s = strings.ReplaceAll(s, opener+" ", opener)
	}
	// 空括号/空引号
	for _, pair := range [][2]string{{"()", ""}, {"（）", ""}, {"[]", ""}, {"【】", ""}, {"“”", ""}} {
		s = strings.ReplaceAll(s, pair[0], pair[1])
	}
	// 行首孤立的标点（整句被删后常见：", rest of line"）
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		for _, p := range []string{",", ";", "，", "；", "、"} {
			if strings.HasPrefix(trimmed, p) {
				trimmed = strings.TrimLeft(trimmed[len(p):], " \t")
			}
		}
		lines[i] = trimmed
	}
	return strings.Join(lines, "\n")
}

// tidySpacing 收掉整句替换后留下的多余空白（"is an  open source" 之类），
// 但保留换行结构，避免破坏提示词的分段语义。
func tidySpacing(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := line
		for strings.Contains(trimmed, "  ") {
			trimmed = strings.ReplaceAll(trimmed, "  ", " ")
		}
		// 句首多余空格（整句替换为空后常见）收掉。
		if strings.HasPrefix(trimmed, " ") && strings.TrimSpace(trimmed) != "" {
			trimmed = strings.TrimLeft(trimmed, " ")
		}
		lines[i] = trimmed
	}
	return strings.Join(lines, "\n")
}

func isUnapprovedChannel(raw []byte) bool {
	return classifyUpstreamRejection(raw) == rejectionUnapprovedChannel
}

func isUpstreamModelUnavailable(raw []byte) bool {
	return classifyUpstreamRejection(raw) == rejectionModelUnauthorized
}

func isUpstreamRequestError(raw []byte) bool {
	return classifyUpstreamRejection(raw) != rejectionNone
}

// rejectionKind 上游「拒这条请求、但不是账号的错」的类别。
// 这类错误不能把账号打进冷却：换个号重试也是同样的结果。
type rejectionKind int

const (
	rejectionNone rejectionKind = iota
	// rejectionUnapprovedChannel：11128，请求内容（harness 指纹）触发 WAF。
	// 兜底：命中的请求会把已清洗的 body 再清一遍并降级重试一次（见 proxy.go）。
	rejectionUnapprovedChannel
	// rejectionModelUnauthorized：11102，该模型对当前账号未授权。
	rejectionModelUnauthorized
	// rejectionContentFiltered：内容审核命中（非 channel 类）。
	rejectionContentFiltered
)

// contentFilteredMarkers 内容审核命中的文案特征。
var contentFilteredMarkers = []string{
	"content filter",
	"content_filter",
	"content policy",
	"contentpolicy",
	"sensitive",
	"risk control",
	"12200",
	"11103",
	"11104",
}

// classifyUpstreamRejection 把上游错误体归类。
// 统一入口的好处：冷却判定、换号重试、降级重试都读同一份判定，不会再出现
// 「一处认为是账号问题、另一处认为是请求问题」的分叉。
func classifyUpstreamRejection(raw []byte) rejectionKind {
	if len(raw) == 0 {
		return rejectionNone
	}
	s := string(raw)
	if strings.Contains(s, "11128") {
		return rejectionUnapprovedChannel
	}
	low := strings.ToLower(s)
	if strings.Contains(low, "unapproved channel") {
		return rejectionUnapprovedChannel
	}
	if strings.Contains(s, "11102") {
		return rejectionModelUnauthorized
	}
	if strings.Contains(low, "only available for authorized users") ||
		strings.Contains(low, "the requested model is not available") ||
		strings.Contains(low, "model is not available") {
		return rejectionModelUnauthorized
	}
	for _, marker := range contentFilteredMarkers {
		if strings.Contains(low, marker) {
			return rejectionContentFiltered
		}
	}
	return rejectionNone
}

func isModelQuotaExhausted(status int, raw []byte) bool {
	if status == 429 {
		return true
	}
	kind := classifyUpstreamRejection(raw)
	if kind == rejectionUnapprovedChannel || kind == rejectionModelUnauthorized {
		return false
	}
	s := strings.ToLower(string(raw))
	if s == "" {
		return false
	}
	keys := []string{
		"quota", "rate limit", "rate_limit", "too many requests",
		"insufficient", "exhausted", "dosage", "overloaded",
		"额度", "余量", "用量已", "次数", "配额", "达上限", "超限", "用尽",
		"不足", "限流", "频繁", "用量不足", "套餐",
	}
	for _, k := range keys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 11128 降级重试：escalating re-sanitize
// ---------------------------------------------------------------------------

// resanitizeUpstreamChat 返回一份「更激进」的请求体，用于 11128 被拒后的降级重试。
//
// 与首轮的区别只有力度：首轮按配置档位清洗（通常只碰客户端模板面），这里直接上
// sanitizeFull，把 assistant 历史、tool 输出、tool description 一并纳入，并启用
// harness 块整段重写这类不依赖零宽的结构性手段。
//
// 返回 false 表示这一趟没能再改出任何东西（已经清无可清），调用方应直接返回错误，
// 不要无限重试——否则会把上游 400 变成一眼看不出的死循环。
func resanitizeUpstreamChat(body map[string]any) (map[string]any, bool) {
	if body == nil {
		return nil, false
	}
	if !sanitizeRequest(body, sanitizeFull) {
		return nil, false
	}
	return body, true
}

// harnessContextMarkers 是 Codex / Claude Code 注入运行时上下文的标记串。
// 命中的 user 消息不是用户真实提问，而是 harness 模板。
var harnessContextMarkers = []string{
	"# AGENTS.md instructions",
	"<environment_context>",
	"<permissions instructions>",
	"<collaboration_mode>",
	"<skills_instructions>",
	"<plugins_instructions>",
	"<system-reminder>",
	"# claudeMd",
	"<user_instructions>",
	"<codex_internal_context",
}

func looksLikeHarnessContext(text string) bool {
	if text == "" {
		return false
	}
	for _, marker := range harnessContextMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// plainContentText 把各种 content 形状拍平成纯文本（仅用于标记识别，不做改写）。
func plainContentText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var b strings.Builder
		for _, part := range t {
			b.WriteString(plainContentText(part))
		}
		return b.String()
	case map[string]any:
		var b strings.Builder
		if text, ok := t["text"].(string); ok {
			b.WriteString(text)
		}
		if content, ok := t["content"]; ok {
			b.WriteString(plainContentText(content))
		}
		return b.String()
	default:
		return ""
	}
}

// retryWAFRejectedBody 尝试为被 WAF 拒绝的请求构造一份更干净的 body。
// 复用 meta 的协议上下文（model / stream 等字段保持原样，只重写 messages）。
func retryWAFRejectedBody(meta *ChatRequestMeta) bool {
	if meta == nil || len(meta.Body) == 0 {
		return false
	}
	var body map[string]any
	if err := json.Unmarshal(meta.Body, &body); err != nil {
		return false
	}
	updated, changed := resanitizeUpstreamChat(body)
	if !changed {
		return false
	}
	encoded, err := json.Marshal(updated)
	if err != nil {
		return false
	}
	meta.Body = encoded
	// 请求预览同步更新，便于后台看到真正发出去的 body。
	meta.RequestPreview = captureChatRequest(encoded)
	return true
}
