package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"codebuddy-gateway/global"
	"codebuddy-gateway/model"
)

// ---------------------------------------------------------------------------
// 成长中心任务 API
//
// 三个端点，全部实测跑通（2026-09-15，CodeBuddy 账号 Ana Renata）：
//   - GET  /v2/activity/growth/tasks                   全量任务列表
//   - POST /v2/activity/growth/tasks/accept            批量报名 {"task_codes":[...]}
//   - POST /activity/growth/tasks/<code>/claim         领奖（任务码在路径、无 body）
//
// 领奖端点必须走 Web 域（www.codebuddy.cn）+ x-client-platform: web。
// 走 CLI 域（copilot.tencent.com）的 /v2/.../reward/claim 会一直返回 400
// "task not completed"——那条路径不存在，是历史上领奖失败的真实原因。
//
// 状态机：accept 是「报名」，本身不产生进度；进度由行为事件点亮。
// 所以正确顺序是 先 accept → 再上报行为事件 → 达标后 claim。
// 实测：未 accept 时 progress.target 恒为 0，上报事件不计数。
// ---------------------------------------------------------------------------

const (
	growthTasksPath  = "/v2/activity/growth/tasks"
	growthAcceptPath = "/v2/activity/growth/tasks/accept"
	reportPath       = "/v2/report"
)

// GrowthTask 任务的对外视图。
type GrowthTask struct {
	TaskCode     string `json:"task_code"`
	Title        string `json:"title,omitempty"`
	Description  string `json:"description,omitempty"`
	TaskDesc     string `json:"task_desc,omitempty"`
	Credit       int64  `json:"credit,omitempty"`
	Energy       int64  `json:"energy,omitempty"`
	TaskType     string `json:"task_type,omitempty"`
	Locked       bool   `json:"locked,omitempty"`
	Target       int64  `json:"target"`
	Current      int64  `json:"current"`
	AcceptStatus string `json:"accept_status,omitempty"`
	Claimable    bool   `json:"claimable,omitempty"`
	Claimed      bool   `json:"claimed,omitempty"`
}

// growthHeaders 构造 growth 域请求头（与网关既有的 CodeBuddy 头对齐）。
func growthHeaders(req *http.Request, acc *model.Account) {
	cb := global.CORE_CONFIG.CodeBuddy
	version := cb.HeaderIDEVersion()
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(acc.JWT))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Domain", cb.HeaderDomain())
	req.Header.Set("X-Product", cb.HeaderProduct())
	req.Header.Set("X-IDE-Type", cb.HeaderIDEType())
	req.Header.Set("X-IDE-Version", version)
	req.Header.Set("X-Product-Version", cb.HeaderProductVersion())
	req.Header.Set("X-Env-ID", cb.HeaderEnvID())
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s CodeBuddy/%s", cb.HeaderIDEType(), version, version))
	if acc.UID() != "" {
		req.Header.Set("X-User-Id", acc.UID())
	}
}

// growthBase / webBase 分别返回任务域与领奖域。领奖只在 Web 域提供。
func growthBase() string { return strings.TrimRight(global.CORE_CONFIG.Gateway.UpstreamBase(), "/") }

func webBase() string {
	base := strings.TrimRight(global.CORE_CONFIG.CodeBuddy.BillingBaseURL(), "/")
	if base == "" {
		return "https://www.codebuddy.cn"
	}
	return base
}

// growthDo 发一次 growth 域请求并解出 data 段。
func (c *UpstreamClient) growthDo(ctx context.Context, acc *model.Account, method, url string, body any) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, err
	}
	growthHeaders(req, acc)
	return c.doGrowthRaw(req)
}

// GrowthListTasks 拉取全量任务列表。
//
// progress 字段有两种形状（对象 {current,target} 或平铺），两种都解。
func (c *UpstreamClient) GrowthListTasks(ctx context.Context, acc *model.Account) ([]GrowthTask, error) {
	data, err := c.growthDo(ctx, acc, http.MethodGet, growthBase()+growthTasksPath, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Tasks []struct {
			TaskCode     string          `json:"task_code"`
			Title        string          `json:"title"`
			Description  string          `json:"description"`
			TaskDesc     string          `json:"task_desc"`
			RewardCredit int64           `json:"reward_credit"`
			RewardEnergy int64           `json:"reward_energy"`
			TaskType     string          `json:"task_type"`
			Locked       bool            `json:"locked"`
			AcceptStatus string          `json:"accept_status"`
			Target       int64           `json:"target"`
			Current      int64           `json:"current"`
			Progress     json.RawMessage `json:"progress"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	out := make([]GrowthTask, 0, len(resp.Tasks))
	for _, t := range resp.Tasks {
		cur, tgt := t.Current, t.Target
		if len(t.Progress) > 0 && string(t.Progress) != "null" {
			var pr struct {
				Current int64 `json:"current"`
				Target  int64 `json:"target"`
			}
			if json.Unmarshal(t.Progress, &pr) == nil && (pr.Target > 0 || pr.Current > 0) {
				cur, tgt = pr.Current, pr.Target
			}
		}
		claimed := t.AcceptStatus == "claimed"
		out = append(out, GrowthTask{
			TaskCode:     t.TaskCode,
			Title:        t.Title,
			Description:  t.Description,
			TaskDesc:     t.TaskDesc,
			Credit:       t.RewardCredit,
			Energy:       t.RewardEnergy,
			TaskType:     t.TaskType,
			Locked:       t.Locked,
			Target:       tgt,
			Current:      cur,
			AcceptStatus: t.AcceptStatus,
			Claimable:    !claimed && tgt > 0 && cur >= tgt,
			Claimed:      claimed,
		})
	}
	return out, nil
}

// GrowthAcceptTasks 批量报名。幂等：已报名时上游返回业务提示，不视为致命错误。
func (c *UpstreamClient) GrowthAcceptTasks(ctx context.Context, acc *model.Account, codes []string) error {
	if len(codes) == 0 {
		return nil
	}
	_, err := c.growthDo(ctx, acc, http.MethodPost, growthBase()+growthAcceptPath,
		map[string]any{"task_codes": codes})
	return err
}

// GrowthClaimReward 领取单个任务奖励，返回本次到账的 (credit, energy)。
//
// 端点：POST {webBase}/activity/growth/tasks/<task_code>/claim
// 任务码在路径、无 body，必须带 x-client-platform: web。已领取时返回 (0,0,nil)（幂等）。
func (c *UpstreamClient) GrowthClaimReward(ctx context.Context, acc *model.Account, taskCode string) (int64, int64, error) {
	url := webBase() + "/activity/growth/tasks/" + taskCode + "/claim"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return 0, 0, err
	}
	growthHeaders(req, acc)
	// Web 端领奖的额外标记：来源端与来源页。缺 x-client-platform 会被拒。
	req.Header.Set("x-client-platform", "web")
	req.Header.Set("Origin", webBase())
	req.Header.Set("Referer", webBase()+"/profile/growth-center")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("http %d: %s", resp.StatusCode, clip(raw, 200))
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			AlreadyClaimed bool  `json:"already_claimed"`
			Credit         int64 `json:"credit"`
			Energy         int64 `json:"energy"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return 0, 0, fmt.Errorf("parse: %w", err)
	}
	if env.Code != 0 {
		return 0, 0, fmt.Errorf("api code %d: %s", env.Code, env.Msg)
	}
	if env.Data.AlreadyClaimed {
		return 0, 0, nil
	}
	return env.Data.Credit, env.Data.Energy, nil
}

// ---------------------------------------------------------------------------
// 行为事件上报
// ---------------------------------------------------------------------------

// ReportChatActivity 上报一条「对话活跃」事件（chat_request_send）。
//
// userId 必填且必须等于账号 uid：实测缺失时上游返回 200 但静默丢弃，
// 事件不计入任务进度。这是最容易踩的坑。
//
// 注意：必须 await accept 之后再上报，否则 progress.target 恒为 0、不计数。
func (c *UpstreamClient) ReportChatActivity(ctx context.Context, acc *model.Account, conversationID, modelID, modelName string) error {
	if conversationID == "" {
		conversationID = fmt.Sprintf("cbgw-%d", time.Now().UnixNano())
	}
	if modelID == "" {
		modelID = "deepseek-v4-flash"
	}
	if modelName == "" {
		modelName = modelID
	}
	now := time.Now().UnixMilli()
	ev := map[string]any{
		"eventCode": "chat_request_send", "timestamp": now, "reportDelay": 0,
		"mode": "craft", "conversationId": conversationID, "requestId": conversationID,
		"inputLength": 12, "requestModelId": modelID, "requestModelName": modelName,
		"isPlan": false, "isAutoExecuteTerminal": false, "isAutoModify": false,
		"codebaseEnable": false, "maxToken": 0, "maxSteps": 0, "temperature": 0,
		"maxRetries": 0, "mentionContexts": []any{}, "knowledgeId": []any{},
		"knowledgeName": []any{}, "codebaseId": "", "mentionContextCount": 0,
		"command": "", "expertId": "", "recommendId": "", "skillId": "",
		"skillCount": 0, "totalCount": 0, "fileUri": "", "presentAt": now,
		"traceId": "", "rootRequestId": conversationID, "parentConversationId": conversationID,
		"agentName": "default", "agentType": "conversation",
		"userId": acc.UID(),
	}
	return c.reportEvents(ctx, acc, []any{ev})
}

// reportEvents 向 /v2/report 批量上报事件数组。
func (c *UpstreamClient) reportEvents(ctx context.Context, acc *model.Account, events []any) error {
	if len(events) == 0 {
		return nil
	}
	raw, err := json.Marshal(events)
	if err != nil {
		return err
	}
	req, err := newJSONPost(ctx, growthBase()+reportPath, raw)
	if err != nil {
		return err
	}
	growthHeaders(req, acc)
	return c.doGrowth(req)
}

// ---------------------------------------------------------------------------
// HTTP 原语
// ---------------------------------------------------------------------------

// newJSONPost 构造一个带 JSON body 的 POST 请求。
func newJSONPost(ctx context.Context, url string, raw []byte) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
}

// webHeaders 注入 Web 来源端标记。缺 x-client-platform 的请求会被上游按
// CLI 端处理，而任务领奖一类接口只在 Web 端开放。
func webHeaders(req *http.Request, referer string) {
	req.Header.Set("x-client-platform", "web")
	req.Header.Set("Origin", webBase())
	if referer == "" {
		referer = webBase() + "/profile/growth-center"
	}
	req.Header.Set("Referer", referer)
}

// doGrowth 发请求并校验信封，不需要 data 时用这个。
func (c *UpstreamClient) doGrowth(req *http.Request) error {
	_, err := c.doGrowthRaw(req)
	return err
}

// doGrowthRaw 发请求、校验 code==0，返回 data 段原始 JSON。
func (c *UpstreamClient) doGrowthRaw(req *http.Request) (json.RawMessage, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, clip(raw, 200))
	}
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("api code %d: %s", env.Code, env.Msg)
	}
	return env.Data, nil
}
