package service

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"codebuddy-gateway/model"
)

// ---------------------------------------------------------------------------
// 行为事件链构造
//
// 任务进度不由 UI 操作驱动，而由客户端上报的**行为事件**点亮。要让服务端
// 认账，事件必须凑成「完整链路」而不是单条——上游会把同一个
// conversationId / requestId 下的事件串起来判定。
//
// 下列序列是对真实客户端上报形状的复刻：公共设备指纹 + 业务事件按序拼接。
// 字段名与取值都按实测口径写，不做「看起来差不多」的精简，
// 因为服务端对字段缺失的容忍度未知，精简版可能静默不计数。
// ---------------------------------------------------------------------------

type desktopEvent map[string]any

// desktopFingerprint 是桌面客户端的公共设备指纹，每条事件都会带上。
//
// machineId / sessionId 由账号 uid 派生而非随机：同账号多次上报保持一致，
// 避免每次上报都表现为「新设备」，那是风控最敏感的信号。
func desktopFingerprint(acc *model.Account) map[string]any {
	now := time.Now().UnixMilli()
	return map[string]any{
		"timezone":     "Asia/Shanghai",
		"reportDelay":  2000,
		"userId":       acc.UID(),
		"username":     acc.Name,
		"userNickname": acc.Name,
		"product":      "SaaS",
		"releaseDate":  int64(1789036585355),
		"commit":       "5f9692923c93033111c51ad7b003eb80204a9b75",
		"ideName":      "WorkBuddy",
		"ideType":      "WorkBuddy",
		"ideVersion":   "5.5.6",
		"machineId":    deriveAccountID(acc, "machine"),
		"sessionId":    deriveAccountID(acc, "session"),
		"extName":      "workbuddy-desktop",
		"extVersion":   "5.5.6",
		"os":           "win32",
		"arch":         "x64",
		"osVersion":    "10.0.26220",
		"cpuCores":     20,
		"memorySize":   24,
		"timestamp":    now,
		"presentAt":    now,
	}
}

// deriveAccountID 由账号标识 + 用途派生出稳定的伪设备 ID。
// 稳定是刻意的：同账号重复上报应表现为同一台设备。
func deriveAccountID(acc *model.Account, purpose string) string {
	seed := acc.UID()
	if seed == "" {
		seed = acc.Username
	}
	if seed == "" {
		seed = fmt.Sprintf("%d", acc.ID)
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed + "|" + purpose))
	return fmt.Sprintf("%016x", h.Sum64())
}

// reportDesktopEvents 以桌面指纹批量上报事件。
func (c *UpstreamClient) reportDesktopEvents(ctx context.Context, acc *model.Account, events ...desktopEvent) error {
	if len(events) == 0 {
		return fmt.Errorf("no events")
	}
	fp := desktopFingerprint(acc)
	arr := make([]any, 0, len(events))
	for _, ev := range events {
		m := make(map[string]any, len(fp)+len(ev))
		for k, v := range fp {
			m[k] = v
		}
		// 业务字段优先，可覆盖指纹里的默认值。
		for k, v := range ev {
			m[k] = v
		}
		arr = append(arr, m)
	}
	return c.reportEvents(ctx, acc, arr)
}

// desktopChatSequence 构造一次「桌面端成功对话」的完整事件链。
// 这条链是多数任务的基础：chat 类任务靠它计数，其余任务把它当 JOIN 上下文。
func desktopChatSequence(conversationID, requestID, messageID, modelID, modelName string) []desktopEvent {
	mk := func(code string, extra map[string]any) desktopEvent {
		ev := desktopEvent{"eventCode": code}
		for k, v := range extra {
			ev[k] = v
		}
		return ev
	}
	return []desktopEvent{
		mk("agent_task_created", map[string]any{
			"source": "LOCAL", "name": "working", "task_target": "local", "mode": "craft",
			"requestModelId": modelID, "requestModelName": modelName,
			"has_repo": false, "repo_type": "none", "workspace_type": "empty",
			"has_connector": false, "connector_types": []any{},
			"has_mention": false, "mention_types": []any{},
			"has_template": false, "action": "", "template_name": "",
			"has_expert": false, "expert_id": "", "expert_name": "", "expert_industry_id": "",
			"has_skill": false, "skill_names": []any{},
			"conversationId": conversationID, "messageId": messageID,
			"buddyId": "", "buddyName": "",
		}),
		mk("chat_message_send", map[string]any{
			"messageId": messageID + "-assistant", "historyCount": 0,
			"isContextTruncated": false, "currentStepCount": 1,
			"traceId": requestID, "rootRequestId": requestID,
			"parentConversationId": conversationID,
			"agentName":            "cli", "agentType": "main",
		}),
		mk("chat_request_send", map[string]any{
			"inputLength": 24, "isPlan": false, "isAutoExecuteTerminal": false,
			"isAutoModify": false, "codebaseEnable": false, "maxToken": 0,
			"maxSteps": 500, "temperature": 0, "maxRetries": 0,
			"mentionContexts": []any{}, "knowledgeId": []any{}, "knowledgeName": []any{},
			"codebaseId": "", "mentionContextCount": 0, "command": "",
			"recommendId": "", "skillId": "", "skillCount": 0, "totalCount": 0,
			"traceId": requestID, "rootRequestId": requestID,
			"parentConversationId": conversationID,
			"agentName":            "cli", "agentType": "main",
			"codebuddy.session_id":              conversationID,
			"codebuddy.conversation_request_id": requestID,
		}),
		mk("chat_message_response", map[string]any{
			"messageId": messageID + "-assistant", "responseModelId": modelID,
			"inputToken": 120, "outputToken": 80, "totalToken": 200,
			"cachedTokens": 0, "cachedWriteTokens": 0, "cachedMissTokens": 0,
			"isSuccessful": true, "finishReason": "stop",
			"durationMs": 1200, "firstTokenMs": 320,
			"traceId": requestID, "rootRequestId": requestID,
			"parentConversationId": conversationID,
		}),
		mk("chat_message_status", map[string]any{
			"messageId": messageID + "-assistant", "status": "completed",
			"traceId": requestID, "rootRequestId": requestID,
			"parentConversationId": conversationID,
		}),
		mk("chat_request_response", map[string]any{
			"conversationId": conversationID, "requestId": requestID,
			"isSuccessful": true, "status": "completed",
			"inputToken": 120, "outputToken": 80, "totalToken": 200,
			"traceId": requestID, "rootRequestId": requestID,
		}),
	}
}

// desktopTemplateUseSequence：「使用模板创建任务」事件组。
func desktopTemplateUseSequence(conversationID, requestID, templateID, templateName string) []desktopEvent {
	events := desktopChatSequence(conversationID, requestID, "msg-"+templateID, "fast-model", "fast-model")
	return append(events,
		desktopEvent{
			"eventCode": "agent_task_created_with_template", "mode": "working",
			"isCustomModel": false, "id": templateID, "name": templateName,
			"requestId": requestID,
		},
		desktopEvent{"eventCode": "template_used", "template_id": templateID, "task_mode": "working"},
	)
}

// desktopPlaybookPromptSequence：「灵感案例做同款」事件组。
func desktopPlaybookPromptSequence(conversationID, requestID, caseID, caseName string) []desktopEvent {
	events := desktopChatSequence(conversationID, requestID, "msg-pb", "fast-model", "fast-model")
	payload := map[string]any{
		"id": caseID, "name": caseName, "type": "document",
		"categoryId": "", "categoryName": "",
	}
	withPayload := func(code string, extra map[string]any) desktopEvent {
		m := map[string]any{"eventCode": code}
		for k, v := range extra {
			m[k] = v
		}
		for k, v := range payload {
			m[k] = v
		}
		return desktopEvent(m)
	}
	return append(events,
		desktopEvent{
			"eventCode": "web_element_click", "pageName": "playbook_detail",
			"elementId": "playbook_ctaClick", "elementName": caseName, "source": "discover",
		},
		withPayload("playbook_cta_click", map[string]any{"source": "discover", "position": 0}),
		withPayload("playbook_prompt_send", map[string]any{
			"conversationId": conversationID, "requestId": requestID,
		}),
	)
}

// desktopDesignCanvasSequence：「设计创意画布创建」事件组。
func desktopDesignCanvasSequence(conversationID, requestID string) []desktopEvent {
	events := desktopChatSequence(conversationID, requestID, "msg-canvas", "fast-model", "fast-model")
	tail := requestID
	if len(tail) > 8 {
		tail = tail[len(tail)-8:]
	}
	return append(events,
		desktopEvent{
			"eventCode": "wbx_design_canvas_task_create", "conversationId": conversationID,
			"requestId": requestID, "source": "summon_keyword", "cost": 12000,
			"isSuccessful": true,
		},
		desktopEvent{
			"eventCode": "wbx_design_canvas_open", "conversationId": conversationID,
			"requestId": requestID, "id": "ardot-file-" + tail,
			"source": "summon_keyword", "type": "page", "cost": 13000,
			"isSuccessful": true,
		},
	)
}

// desktopBuddyAppSequence：「进入 Buddy 应用」五连事件。
//
// 同一组事件同时覆盖 Buddy_App（进入任一应用）与 Buddy_App_QQ（企鹅教师助手）。
//
// 字段是照实测口径逐个对齐的：mode=LOCAL、buddyId/buddyName 出现在**每条**事件上、
// 事件码带 _click 后缀（buddyapp_auth_confirm_click 而非 ..._confirm）。
// 这些看着琐碎，但服务端按字段与事件码精确匹配，少一个就不计数
// （实测：漏掉 mode 与 elementId 时进度停在 0/1）。
func desktopBuddyAppSequence(buddyID, buddyName string) []desktopEvent {
	mk := func(code string, extra map[string]any) desktopEvent {
		ev := desktopEvent{
			"eventCode": code, "mode": "LOCAL",
			"buddyId": buddyID, "buddyName": buddyName,
		}
		for k, v := range extra {
			ev[k] = v
		}
		return ev
	}
	return []desktopEvent{
		mk("buddyapp_discover_click", nil),
		mk("buddyapp_show", map[string]any{
			"elementId": buddyID, "elementName": buddyName, "position": 2,
		}),
		mk("buddyapp_enter_click", map[string]any{
			"elementId": buddyID, "elementName": buddyName, "position": 2, "isFirstPage": "1",
		}),
		mk("buddyapp_auth_confirm_click", map[string]any{
			"elementId": buddyID, "elementName": buddyName,
		}),
		mk("buddyapp_bindaccount_skip_click", map[string]any{
			"elementId": buddyID, "elementName": buddyName,
		}),
	}
}

// desktopAutomationCreateEvent：「设置自动化任务」事件。
func desktopAutomationCreateEvent(name string) desktopEvent {
	return desktopEvent{
		"eventCode": "automated_task_create_suc", "name": name,
		"isSuccessful": true, "source": "settings",
	}
}

// webEvent 是 Web 域事件（不同于桌面指纹，走 x-client-platform: web）。
func (c *UpstreamClient) reportWebEvent(ctx context.Context, acc *model.Account, eventCode, pageURL, elementID, elementName string) error {
	const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"
	ev := map[string]any{
		"eventCode": eventCode, "timestamp": time.Now().UnixMilli(), "reportDelay": 0,
		"pageURL": pageURL, "elementId": elementID, "elementName": elementName,
		"os": "Win32", "arch": "", "osVersion": "10.0", "userAgent": ua,
		"machineId":    deriveAccountID(acc, "webmachine"),
		"userId":       acc.UID(),
		"userNickname": acc.Name,
	}
	// Web 事件同样走 /v2/report，但来源域是官网。
	return c.reportEventsWeb(ctx, acc, []any{ev}, pageURL)
}

// reportEventsWeb 以 Web 端身份上报（补 x-client-platform / Origin / Referer）。
func (c *UpstreamClient) reportEventsWeb(ctx context.Context, acc *model.Account, events []any, referer string) error {
	raw, err := json.Marshal(events)
	if err != nil {
		return err
	}
	req, err := newJSONPost(ctx, webBase()+reportPath, raw)
	if err != nil {
		return err
	}
	growthHeaders(req, acc)
	webHeaders(req, referer)
	return c.doGrowth(req)
}

// setAppearanceTheme 设置外观主题（Hp_Appearance 的判据之一）。
//
// 端点是 /v2/user-asset/appearance/set —— 不是看起来更"合理"的
// /v2/plugin/appearance/set（那条路径 404）。实测确认。
func (c *UpstreamClient) setAppearanceTheme(ctx context.Context, acc *model.Account, resourceKey string) error {
	raw, err := json.Marshal(map[string]string{"kind": "theme", "resource_key": resourceKey})
	if err != nil {
		return err
	}
	req, err := newJSONPost(ctx, growthBase()+"/v2/user-asset/appearance/set", raw)
	if err != nil {
		return err
	}
	growthHeaders(req, acc)
	return c.doGrowth(req)
}

// marketExpert 专家市场的单个专家。
type marketExpert struct {
	ExpertID      string `json:"expert_id"`
	ExpertType    string `json:"expert_type"`
	DisplayNameZH string `json:"display_name_zh"`
	ProfessionZH  string `json:"profession_zh"`
	Version       string `json:"version"`
}

// marketExpertList 拉取专家市场列表。专家 ID 必须真实存在，自造 ID 不计数。
func (c *UpstreamClient) marketExpertList(ctx context.Context, acc *model.Account, expertType string) ([]marketExpert, error) {
	body := map[string]any{"page": 1, "page_size": 20, "sort_by": "reco_rank", "sort_order": "desc"}
	if expertType != "" {
		body["expert_type"] = expertType
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := newJSONPost(ctx, growthBase()+"/portal/operation-platform/market/expert/list", raw)
	if err != nil {
		return nil, err
	}
	growthHeaders(req, acc)
	data, err := c.doGrowthRaw(req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Experts []marketExpert `json:"experts"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out.Experts, nil
}

// expertSummonSequence：专家召唤链。
func expertSummonSequence(e marketExpert) []desktopEvent {
	ver := e.Version
	if ver == "" {
		ver = "1.0.0"
	}
	return []desktopEvent{
		{
			"eventCode": "web_element_click", "source": e.ExpertID, "type": "expert-all",
			"version": ver, "elementId": "expert_summon_click", "elementName": "立即召唤",
			"pageURL": "/C:/Program%20Files/WorkBuddy/resources/app.asar/renderer/index.html",
		},
		{
			"eventCode": "expert_summon_click", "id": e.ExpertID, "name": e.DisplayNameZH,
			"expertTitle": e.ProfessionZH, "type": "expert-all", "position": 0,
			"expertType": e.ExpertType, "version": ver, "mode": "LOCAL",
		},
		{
			"eventCode": "expert_summoned", "id": e.ExpertID, "name": e.DisplayNameZH,
			"expertTitle": e.ProfessionZH, "type": "expert-all",
		},
	}
}

// expertActualUseEvent：专家「真实使用」事件（expert_5 / Expert_team_use_3 的判据）。
// requestID 必须是真实 chat 返回的服务端 ID，自造不计数。
func expertActualUseEvent(e marketExpert, conversationID, requestID, mode string) desktopEvent {
	ver := e.Version
	if ver == "" {
		ver = "1.0.0"
	}
	return desktopEvent{
		"eventCode": "expert_actual_use", "id": e.ExpertID, "name": e.DisplayNameZH,
		"expertTitle": e.ProfessionZH, "expertType": e.ExpertType, "version": ver,
		"conversationId": conversationID, "requestId": requestID,
		"mode": mode, "isSuccessful": true,
	}
}

// sanitizeIDForEvent 把 ID 限制在事件字段可接受的字符集内。
// conversationId 会直接进 JSON，不能带引号/换行。
func sanitizeIDForEvent(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_':
			return r
		default:
			return -1
		}
	}, s)
}

// newEventIDs 生成一组同源的事件 ID（会话 / 请求 / 消息）。
func newEventIDs(prefix string) (conversationID, requestID, messageID string) {
	base := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	base = sanitizeIDForEvent(base)
	return "conv-" + base, "req-" + base, "msg-" + base
}
