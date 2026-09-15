package service

import (
	"fmt"
	"strings"
	"time"

	"codebuddy-gateway/global"
	"codebuddy-gateway/model"

	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// 任务自动完成
//
// 执行顺序有讲究：部分任务互为前置，必须按依赖顺序跑。
//   - first_buddy（领养）依赖当日活跃事件解锁，所以自带一次 chat 上报。
//   - 其余任务靠各自的事件链独立计数。
//
// 每个动作都设计成幂等：重复执行不会重复加分（上游对已 claimed 的任务
// 返回 already_claimed），所以「一键完成」可以放心重复点。
// ---------------------------------------------------------------------------

// taskRunner 单个任务的执行器。
type taskRunner struct {
	// Code 目标任务码。
	Code string
	// Desc 展示用说明。
	Desc string
	// Attempt 标记「尝试型」：上游未证实可完全脚本化，跑了可能不点亮。
	Attempt bool
	run     func(c *UpstreamClient, acc *model.Account) (string, error)
}

// reportGap 两次事件上报之间的最小间隔。
// 上游对事件流有节奏判定，连续无间隔上报容易被判为异常流量。
const reportGap = 1100 * time.Millisecond

// expertSummonGap 专家召唤链的间隔（真实使用节奏，实测该间隔成功率最高）。
const expertSummonGap = 6 * time.Second

// taskRunners 可自动化任务的动作表，顺序即执行顺序。
var taskRunners = []taskRunner{
	{Code: "chat_5", Desc: "上报 5 条对话活跃事件", run: runChat5},
	{Code: "first_buddy", Desc: "同意协议并领取第一只 Buddy", run: runFirstBuddy},
	{Code: "Model_chat_GLM5.2", Desc: "glm-5.2 真实对话并对齐模型上报", run: runModelChat},
	{Code: "RichMeow_Chat", Desc: "桌面端对话事件链", run: runRichMeowChat},
	{Code: "Buddy_App", Desc: "进入 Buddy 应用事件链", run: runBuddyApp},
	{Code: "Buddy_App_QQ", Desc: "进入企鹅教师助手事件链", run: runBuddyApp},
	{Code: "automation_1", Desc: "创建自动化任务事件", run: runAutomationCreate},
	{Code: "Library_read", Desc: "阅读资料库介绍事件", run: runLibraryRead},
	{Code: "template_5", Desc: "使用模板创建任务 ×5", run: runTemplateUse},
	{Code: "playbook_prompt", Desc: "灵感案例发送 Prompt", run: runPlaybookPrompt},
	{Code: "create_canvas", Desc: "设计创意模式创建画布", run: runCreateCanvas},
	{Code: "expert_5", Desc: "召唤并使用 5 位专家", run: runExpertUse},
	{Code: "Expert_team_use_3", Desc: "召唤并使用 3 个专家团", run: runExpertTeamUse},
	{Code: "Hp_Appearance", Desc: "设置主题并上报皮肤生效", Attempt: true, run: runAppearance},
	{Code: "skill_1", Desc: "真实对话 + 技能加载事件", run: runSkillFresh},
	{Code: "Expert_lighthouse", Desc: "召唤并使用轻量云专家", run: runLighthouse},
	{Code: "black_cat", Desc: "夜猫子：夜间窗口内对话补足", Attempt: true, run: runBlackCat},
}

// runnerFor 查任务对应的执行器。
func runnerFor(code string) *taskRunner {
	for i := range taskRunners {
		if strings.EqualFold(taskRunners[i].Code, strings.TrimSpace(code)) {
			return &taskRunners[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 各任务实现
// ---------------------------------------------------------------------------

// runChat5 上报 5 条对话活跃事件（按当前进度补足差额）。
func runChat5(c *UpstreamClient, acc *model.Account) (string, error) {
	t, err := taskByCode(c, acc, "chat_5")
	if err != nil {
		return "", err
	}
	target := int64(5)
	if t != nil && t.Target > 0 {
		target = t.Target
	}
	need := target
	if t != nil {
		need = target - t.Current
	}
	if need <= 0 {
		return "进度已达标，无需上报", nil
	}
	for i := int64(0); i < need; i++ {
		cid := fmt.Sprintf("cbgw-chat5-%d-%d", time.Now().UnixMilli(), i)
		if err := c.ReportChatActivity(acc, cid, "", ""); err != nil {
			return fmt.Sprintf("上报第 %d/%d 条失败: %v", i+1, need, err), nil
		}
		if i < need-1 {
			time.Sleep(reportGap)
		}
	}
	return fmt.Sprintf("已补报 %d 条对话事件", need), nil
}

// runFirstBuddy 领养首只 Buddy：先上报活跃（解锁前置）→ 同意协议 → 领养。
func runFirstBuddy(c *UpstreamClient, acc *model.Account) (string, error) {
	if err := c.ReportChatActivity(acc, fmt.Sprintf("cbgw-adopt-%d", time.Now().UnixMilli()), "", ""); err != nil {
		return "", fmt.Errorf("前置上报: %w", err)
	}
	time.Sleep(reportGap)
	if err := c.buddyAgreement(acc); err != nil {
		return "", fmt.Errorf("同意协议: %w", err)
	}
	if err := c.buddyFirst(acc); err != nil {
		return fmt.Sprintf("前置已上报，但领养未成功（可能需当日活跃）：%v", err), nil
	}
	return "已领取 Buddy", nil
}

// runModelChat 完成指定模型的真实对话并对齐上报。
func runModelChat(c *UpstreamClient, acc *model.Account) (string, error) {
	const code, modelID, modelName = "Model_chat_GLM5.2", "glm-5.2", "GLM-5.2"
	// accept 失败不阻塞：行为事件才是进度的唯一判据。
	if err := c.GrowthAcceptTasks(acc, []string{code}); err != nil {
		global.CORE_LOG.Warn("task accept failed", zap.String("code", code), zap.Error(err))
	}
	time.Sleep(reportGap)
	conv, reqID, err := c.realChat(acc, modelID, "hi，请回复一句话")
	if err != nil {
		return "", fmt.Errorf("对话请求: %w", err)
	}
	time.Sleep(reportGap)
	if err := c.ReportChatActivity(acc, conv, modelID, modelName); err != nil {
		return "对话已完成，但进度上报失败：" + err.Error(), nil
	}
	_ = reqID
	return "已完成 glm-5.2 对话并上报", nil
}

// runRichMeowChat 桌面端对话完整事件链（RichMeow_Chat 判据）。
func runRichMeowChat(c *UpstreamClient, acc *model.Account) (string, error) {
	conv, req, msg := newEventIDs("cbgw-rm")
	events := desktopChatSequence(conv, req, msg, "fast-model", "fast-model")
	if err := c.reportDesktopEvents(acc, events...); err != nil {
		return "", err
	}
	return "已上报桌面端完整对话事件链", nil
}

// runBuddyApp Buddy 应用进入事件链（同时覆盖 Buddy_App 与 Buddy_App_QQ）。
func runBuddyApp(c *UpstreamClient, acc *model.Account) (string, error) {
	events := desktopBuddyAppSequence("cb_y5Dy46tPQGGWtueMxXbe", "企鹅教师助手")
	if err := c.reportDesktopEvents(acc, events...); err != nil {
		return "", err
	}
	return "已上报 Buddy 应用进入事件链", nil
}

// runAutomationCreate 自动化任务创建事件。
func runAutomationCreate(c *UpstreamClient, acc *model.Account) (string, error) {
	if err := c.reportDesktopEvents(acc, desktopAutomationCreateEvent("cbgw 自动化")); err != nil {
		return "", err
	}
	return "已上报自动化任务创建事件", nil
}

// runLibraryRead 资料库介绍阅读事件（Web 域）。
func runLibraryRead(c *UpstreamClient, acc *model.Account) (string, error) {
	const docURL = "https://www.codebuddy.cn/space/d/intro"
	if err := c.reportWebEvent(acc, "web_element_click", docURL,
		"library_doc_intro_click", "资料库介绍"); err != nil {
		return "", err
	}
	return "已上报资料库介绍阅读事件", nil
}

// runTemplateUse 使用模板创建任务 ×5。
func runTemplateUse(c *UpstreamClient, acc *model.Account) (string, error) {
	templates := [][2]string{
		{"1", "深度研究"}, {"2", "周报生成"}, {"3", "竞品分析"},
		{"4", "活动策划"}, {"5", "代码评审"},
	}
	for i, tp := range templates {
		conv, req, _ := newEventIDs(fmt.Sprintf("cbgw-tpl%d", i))
		events := desktopTemplateUseSequence(conv, req, tp[0], tp[1])
		if err := c.reportDesktopEvents(acc, events...); err != nil {
			return fmt.Sprintf("第 %d 组模板事件上报失败: %v", i+1, err), nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return "已上报 template_used ×5", nil
}

// runPlaybookPrompt 灵感案例发送 Prompt。
func runPlaybookPrompt(c *UpstreamClient, acc *model.Account) (string, error) {
	conv, req, _ := newEventIDs("cbgw-pb")
	events := desktopPlaybookPromptSequence(conv, req, "pm-gtm-launch-plan", "新产品上市 GTM 发布计划一页纸")
	if err := c.reportDesktopEvents(acc, events...); err != nil {
		return "", err
	}
	return "已上报灵感案例 Prompt 发送事件", nil
}

// runCreateCanvas 设计创意模式创建画布。
func runCreateCanvas(c *UpstreamClient, acc *model.Account) (string, error) {
	conv, req, _ := newEventIDs("cbgw-canvas")
	events := desktopDesignCanvasSequence(conv, req)
	if err := c.reportDesktopEvents(acc, events...); err != nil {
		return "", err
	}
	return "已上报设计画布创建事件组", nil
}

// runAppearance 设置主题并上报皮肤生效。
//
// 注意：这个任务是**尝试型**。主题设置接口本身可用
// （POST /v2/user-asset/appearance/set 返回 200），但判据似乎要求客户端
// 在切主题后真实活跃一段时间，纯 API 调用未必点亮。保留实现是为了让
// 账号侧配置就位（真客户端随后使用时即可满足条件）。
func runAppearance(c *UpstreamClient, acc *model.Account) (string, error) {
	const themeKey = "theme-tkmw7j" // 和平精英激战金秋（Hp_Appearance 判据主题）
	if err := c.setAppearanceTheme(acc, themeKey); err != nil {
		return "", fmt.Errorf("设置主题: %w", err)
	}
	time.Sleep(2 * time.Second)
	if err := c.reportDesktopEvents(acc, desktopEvent{
		"eventCode": "appearance_skin_apply", "action": "apply", "source": "settings_close",
		"id": themeKey, "vipLevel": 0, "series": "", "type": "unknown",
	}); err != nil {
		return "", err
	}
	return "已设置主题并上报皮肤生效事件（该任务可能需客户端活跃才计分）", nil
}

// runSkillFresh 真实对话 + skill_info 技能加载事件。
func runSkillFresh(c *UpstreamClient, acc *model.Account) (string, error) {
	conv, req, err := c.realChat(acc, "fast-model", "1+1等于几？直接回答。")
	if err != nil {
		return "", fmt.Errorf("真实对话: %w", err)
	}
	msgID := "msg-" + tail(req, 8)
	events := desktopChatSequence(conv, req, msgID, "fast-model", "fast-model")
	for _, ev := range events {
		if ev["eventCode"] == "chat_message_response" {
			ev["finishReason"] = "tool_calls" // 模型发起工具调用（技能加载）语义
		}
	}
	events = append(events, desktopEvent{
		"eventCode":      "skill_info",
		"id":             "润泽小馆·日报撰写",
		"skillId":        "skill_2097350077599879168",
		"skillVersion":   "1.0.0",
		"toolStatus":     "success",
		"fileCount":      56,
		"source":         "workbuddy-desktop",
		"conversationId": conv, "requestId": req, "messageId": msgID,
		"requestModelId": "fast-model", "requestModelName": "fast-model",
		"traceId": req,
	})
	if err := c.reportDesktopEvents(acc, events...); err != nil {
		return "", fmt.Errorf("skill_info 事件: %w", err)
	}
	return "已上报真实对话 + 技能加载事件", nil
}

// runExpertUse 使用 5 位平台专家。
func runExpertUse(c *UpstreamClient, acc *model.Account) (string, error) {
	return runExpertBatch(c, acc, "agent", 5)
}

// runExpertTeamUse 使用 3 个专家团。
func runExpertTeamUse(c *UpstreamClient, acc *model.Account) (string, error) {
	return runExpertBatch(c, acc, "team", 3)
}

// runExpertBatch 专家召唤 + 使用的公共实现（失败逐个继续）。
func runExpertBatch(c *UpstreamClient, acc *model.Account, expertType string, count int) (string, error) {
	experts, err := c.marketExpertList(acc, expertType)
	if err != nil {
		return "", fmt.Errorf("拉取专家列表: %w", err)
	}
	if len(experts) == 0 {
		return "", fmt.Errorf("专家市场列表为空")
	}
	ok := 0
	for i, e := range experts {
		if ok >= count {
			break
		}
		if err := c.reportDesktopEvents(acc, expertSummonSequence(e)...); err != nil {
			continue
		}
		conv, req, err := c.realChat(acc, "fast-model", "1+1等于几？直接回答。")
		if err != nil {
			continue
		}
		events := append(
			desktopChatSequence(conv, req, "msg-"+tail(req, 8), "fast-model", "fast-model"),
			expertActualUseEvent(e, conv, req, "craft"),
		)
		if err := c.reportDesktopEvents(acc, events...); err != nil {
			continue
		}
		ok++
		if i < len(experts)-1 {
			time.Sleep(expertSummonGap)
		}
	}
	return fmt.Sprintf("已对 %d 位专家完成召唤+使用链（类型 %s）", ok, expertType), nil
}

// runLighthouse 轻量云专家（mode 必须是 LOCAL，与 expert_5 的 craft 不同）。
func runLighthouse(c *UpstreamClient, acc *model.Account) (string, error) {
	const lhID = "ex_2cvvUZQhDyeJ"
	lh := marketExpert{
		ExpertID: lhID, ExpertType: "agent",
		DisplayNameZH: "腾讯轻量云专家", ProfessionZH: "腾讯轻量云专家", Version: "1.0.2",
	}
	if experts, err := c.marketExpertList(acc, "agent"); err == nil {
		for _, e := range experts {
			if e.ExpertID == lhID {
				lh = e
				break
			}
		}
	}
	if err := c.reportDesktopEvents(acc, expertSummonSequence(lh)...); err != nil {
		return "", err
	}
	time.Sleep(reportGap)
	conv, req, err := c.realChat(acc, "fast-model", "1+1等于几？直接回答。")
	if err != nil {
		return "", fmt.Errorf("真实对话: %w", err)
	}
	events := append(
		desktopChatSequence(conv, req, "msg-"+tail(req, 8), "fast-model", "fast-model"),
		expertActualUseEvent(lh, conv, req, "LOCAL"),
	)
	if err := c.reportDesktopEvents(acc, events...); err != nil {
		return "", err
	}
	return "已对轻量云专家完成召唤+使用链", nil
}

// runBlackCat 夜猫子：仅在 23:00–08:00 窗口内计数。
func runBlackCat(c *UpstreamClient, acc *model.Account) (string, error) {
	if !inNightWindow(time.Now()) {
		return "当前不在 23:00–08:00 计数窗口，稍后重试", nil
	}
	t, err := taskByCode(c, acc, "black_cat")
	if err != nil {
		return "", err
	}
	need := int64(1)
	if t != nil {
		need = t.Target - t.Current
	}
	if need <= 0 {
		return "进度已达标，无需补足", nil
	}
	for i := int64(0); i < need; i++ {
		conv, _, err := c.realChat(acc, "glm-5.2", "hi")
		if err != nil {
			return fmt.Sprintf("完成 %d/%d 次后中断: %v", i, need, err), nil
		}
		if err := c.ReportChatActivity(acc, conv, "glm-5.2", "GLM-5.2"); err != nil {
			return fmt.Sprintf("完成 %d/%d 次后上报失败: %v", i, need, err), nil
		}
		if i < need-1 {
			time.Sleep(reportGap)
		}
	}
	return fmt.Sprintf("已完成 %d 次夜间对话并上报", need), nil
}

// inNightWindow 判断是否处于夜猫子计数窗口（23:00–08:00）。
func inNightWindow(t time.Time) bool {
	h := t.Hour()
	return h >= 23 || h < 8
}

// taskByCode 查单个任务当前状态，任务不存在时返回 (nil, nil)。
func taskByCode(c *UpstreamClient, acc *model.Account, code string) (*GrowthTask, error) {
	tasks, err := c.GrowthListTasks(acc)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		if strings.EqualFold(tasks[i].TaskCode, code) {
			return &tasks[i], nil
		}
	}
	return nil, nil
}

// tail 取字符串末尾 n 个字符（不足则原样返回）。
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
