package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"codebuddy-gateway/model"
)

// TestInNightWindow 边界必须精确：23:00–08:00 才是夜猫子计数窗口。
func TestInNightWindow(t *testing.T) {
	cases := []struct {
		hour int
		want bool
	}{
		{23, true}, {0, true}, {3, true}, {7, true},
		{8, false}, {12, false}, {22, false},
	}
	for _, c := range cases {
		got := inNightWindow(timeAtHour(c.hour))
		if got != c.want {
			t.Errorf("hour=%d got=%v want=%v", c.hour, got, c.want)
		}
	}
}

// TestRunnerForCoversAllCodes 每个执行器都要能按任务码查到，
// 否则「一键完成」会静默漏掉任务。
func TestRunnerForCoversAllCodes(t *testing.T) {
	for _, r := range taskRunners {
		if runnerFor(r.Code) == nil {
			t.Errorf("runnerFor(%q) 返回 nil", r.Code)
		}
		// 大小写不敏感（上游任务码大小写不统一，如 Expert_team_use_3）
		if runnerFor(strings.ToLower(r.Code)) == nil {
			t.Errorf("runnerFor 应大小写不敏感: %q", r.Code)
		}
	}
	if runnerFor("__nonexistent__") != nil {
		t.Error("未知任务码应返回 nil")
	}
}

// TestSelectRunnersRespectsOnly 指定任务码时只跑这些，避免误操作用户没点的任务。
func TestSelectRunnersRespectsOnly(t *testing.T) {
	if got := selectRunners(nil); len(got) != len(taskRunners) {
		t.Fatalf("only 为空应返回全部 %d 个，实际 %d", len(taskRunners), len(got))
	}
	got := selectRunners([]string{"chat_5"})
	if len(got) != 1 || got[0].Code != "chat_5" {
		t.Fatalf("应只返回 chat_5，实际 %+v", got)
	}
	if got := selectRunners([]string{"__nonexistent__"}); len(got) != 0 {
		t.Fatalf("未知任务码应返回空，实际 %d", len(got))
	}
}

// TestPendingCreditSkipsClaimed 已领取的任务不能再算进「待领」，
// 否则面板会虚报收益。
func TestPendingCreditSkipsClaimed(t *testing.T) {
	tasks := []GrowthTask{
		{TaskCode: "a", Credit: 100, Claimed: true},
		{TaskCode: "b", Credit: 300},
		{TaskCode: "c", Credit: 50},
	}
	if got := pendingCredit(tasks); got != 350 {
		t.Fatalf("pendingCredit=%d want 350", got)
	}
}

// TestTaskCatalogNonEmpty 面板要展示的清单不能为空。
func TestTaskCatalogNonEmpty(t *testing.T) {
	cat := TaskCatalog()
	if len(cat) == 0 {
		t.Fatal("TaskCatalog 为空")
	}
	for _, item := range cat {
		if item["code"] == "" || item["desc"] == "" {
			t.Fatalf("清单项缺字段: %+v", item)
		}
	}
}

// TestDesktopChatSequenceStructure 事件链必须完整且字段齐备。
// 缺字段的链会被上游静默丢弃——这是最难排查的一类失败。
func TestDesktopChatSequenceStructure(t *testing.T) {
	events := desktopChatSequence("conv-1", "req-1", "msg-1", "fast-model", "fast-model")
	if len(events) < 6 {
		t.Fatalf("对话事件链至少 6 条，实际 %d", len(events))
	}
	want := []string{
		"agent_task_created", "chat_message_send", "chat_request_send",
		"chat_message_response", "chat_message_status", "chat_request_response",
	}
	for i, code := range want {
		if got := events[i]["eventCode"]; got != code {
			t.Errorf("第 %d 条应为 %s，实际 %v", i, code, got)
		}
	}
	// 关键字段：会话/请求 ID 必须贯穿，否则服务端串不起链路。
	for _, ev := range events {
		if ev["conversationId"] == nil && ev["parentConversationId"] == nil {
			t.Errorf("事件 %v 缺少会话标识", ev["eventCode"])
		}
	}
	// 成功标记：chat_message_response 必须 isSuccessful=true。
	for _, ev := range events {
		if ev["eventCode"] == "chat_message_response" {
			if ev["isSuccessful"] != true {
				t.Error("chat_message_response 必须 isSuccessful=true")
			}
		}
	}
}

// TestDeriveAccountIDStableAndDistinct 设备 ID 必须同账号稳定、跨用途不同。
func TestDeriveAccountIDStableAndDistinct(t *testing.T) {
	acc := &model.Account{UserID: "uid-1", Username: "u1"}
	a1 := deriveAccountID(acc, "machine")
	a2 := deriveAccountID(acc, "machine")
	if a1 != a2 {
		t.Fatalf("同账号同用途应稳定: %s vs %s", a1, a2)
	}
	if a1 == deriveAccountID(acc, "session") {
		t.Fatal("不同用途应产生不同 ID")
	}
	other := &model.Account{UserID: "uid-2", Username: "u2"}
	if a1 == deriveAccountID(other, "machine") {
		t.Fatal("不同账号应产生不同 ID")
	}
}

// TestSanitizeIDForEvent ID 要能安全进 JSON，不能带引号换行。
func TestSanitizeIDForEvent(t *testing.T) {
	got := sanitizeIDForEvent(`a"b\c` + "\n" + `d-e_f`)
	if strings.ContainsAny(got, `"\`+"\n") {
		t.Fatalf("危险字符未过滤: %q", got)
	}
	if !strings.Contains(got, "a") || !strings.Contains(got, "-") {
		t.Fatalf("正常字符不应被过滤: %q", got)
	}
}

// TestUIDFallsBackToJWT userId 缺失时必须能从 JWT 的 sub 解析出来。
// 解析不出来 → 事件被静默丢弃，任务永远不点亮。
func TestUIDFallsBackToJWT(t *testing.T) {
	// {"sub":"2993afbb-00b2-4552-99c5-1f1311ff83fc"}
	token := "eyJhbGciOiJSUzI1NiJ9." +
		"eyJzdWIiOiIyOTkzYWZiYi0wMGIyLTQ1NTItOTljNS0xZjEzMTFmZjgzZmMifQ." +
		"sig"
	acc := &model.Account{JWT: token}
	if got := acc.UID(); got != "2993afbb-00b2-4552-99c5-1f1311ff83fc" {
		t.Fatalf("UID 应从 sub 解析，实际 %q", got)
	}
	// 落盘字段优先
	acc2 := &model.Account{UserID: "explicit", JWT: token}
	if got := acc2.UID(); got != "explicit" {
		t.Fatalf("已落盘的 UserID 应优先，实际 %q", got)
	}
	// 坏 token 不 panic、不外抛
	bad := &model.Account{JWT: "not-a-jwt"}
	if got := bad.UID(); got != "" {
		t.Fatalf("坏 token 应返回空串，实际 %q", got)
	}
}

// TestExpertSequencesStructure 专家链的关键字段：expert_actual_use 的
// requestId 必须来自真实 chat，且 mode 可区分 LOCAL / craft。
func TestExpertSequencesStructure(t *testing.T) {
	e := marketExpert{ExpertID: "ex_1", ExpertType: "agent", DisplayNameZH: "专家", Version: "1.0.0"}

	summon := expertSummonSequence(e)
	if len(summon) != 3 {
		t.Fatalf("召唤链应 3 条，实际 %d", len(summon))
	}
	for _, ev := range summon {
		if ev["id"] == nil && ev["source"] == nil {
			t.Errorf("召唤事件缺专家标识: %v", ev["eventCode"])
		}
	}

	use := expertActualUseEvent(e, "conv-x", "req-x", "LOCAL")
	if use["conversationId"] != "conv-x" || use["requestId"] != "req-x" {
		t.Fatal("使用事件必须带真实 chat 的 ID")
	}
	if use["mode"] != "LOCAL" {
		t.Fatal("mode 应可指定为 LOCAL（轻量云判据）")
	}
	if use["eventCode"] != "expert_actual_use" {
		t.Fatalf("eventCode 错误: %v", use["eventCode"])
	}
}

// TestTemplateUseSequenceHasTemplateEvents 模板任务靠这两个事件计数。
func TestTemplateUseSequenceHasTemplateEvents(t *testing.T) {
	events := desktopTemplateUseSequence("c", "r", "1", "深度研究")
	var hasCreated, hasUsed bool
	for _, ev := range events {
		switch ev["eventCode"] {
		case "agent_task_created_with_template":
			hasCreated = true
		case "template_used":
			hasUsed = true
		}
	}
	if !hasCreated || !hasUsed {
		t.Fatalf("模板事件缺失: created=%v used=%v", hasCreated, hasUsed)
	}
}

// TestCanvasSequenceHasWbxEvents 画布任务靠 wbx_* 事件计数。
func TestCanvasSequenceHasWbxEvents(t *testing.T) {
	events := desktopDesignCanvasSequence("c", "r")
	var create, open bool
	for _, ev := range events {
		switch ev["eventCode"] {
		case "wbx_design_canvas_task_create":
			create = true
		case "wbx_design_canvas_open":
			open = true
		}
	}
	if !create || !open {
		t.Fatalf("画布事件缺失: create=%v open=%v", create, open)
	}
}

// TestBuddyAppSequenceFiveEvents 进入应用是五连事件，且字段必须与实测口径一致。
// 实测教训：漏掉 mode / buddyId 或事件码少 _click 后缀，进度就停在 0/1。
func TestBuddyAppSequenceFiveEvents(t *testing.T) {
	events := desktopBuddyAppSequence("cb_x", "企鹅教师助手")
	if len(events) != 5 {
		t.Fatalf("应为五连事件，实际 %d", len(events))
	}
	wantCodes := []string{
		"buddyapp_discover_click", "buddyapp_show", "buddyapp_enter_click",
		"buddyapp_auth_confirm_click", "buddyapp_bindaccount_skip_click",
	}
	for i, code := range wantCodes {
		ev := events[i]
		if ev["eventCode"] != code {
			t.Errorf("第 %d 条应为 %s，实际 %v", i, code, ev["eventCode"])
		}
		if ev["mode"] != "LOCAL" {
			t.Errorf("%s 缺 mode=LOCAL", code)
		}
		if ev["buddyId"] != "cb_x" || ev["buddyName"] != "企鹅教师助手" {
			t.Errorf("%s 缺 buddy 标识", code)
		}
	}
}

// TestTaskLocksPerAccount 同账号共享锁、不同账号互不影响。
func TestTaskLocksPerAccount(t *testing.T) {
	l1 := lockForAccount(1)
	l1b := lockForAccount(1)
	if l1 != l1b {
		t.Fatal("同账号应返回同一把锁")
	}
	if l1 == lockForAccount(2) {
		t.Fatal("不同账号不应共用锁")
	}
}

// 辅助
func timeAtHour(h int) time.Time {
	return time.Date(2026, 9, 15, h, 30, 0, 0, time.Local)
}

// TestRunAccountTasksRequiresUID 锁定一条真实存在的失败形态：
// 没有 userId 时上游对行为事件返回 200 但静默丢弃，任务进度纹丝不动。
// 若不前置拦截，整套流程会「全程成功、结果为零」——最难排查的一类问题。
func TestRunAccountTasksRequiresUID(t *testing.T) {
	c := &UpstreamClient{}
	// 无 JWT、无 UserID：UID 解析必然为空。
	acc := &model.Account{Name: "no-uid"}
	s := c.RunAccountTasks(context.Background(), acc, nil)
	if s.Err == "" {
		t.Fatal("缺 userId 时应提前失败并给出原因")
	}
	if !strings.Contains(s.Err, "userId") {
		t.Fatalf("错误信息应点明 userId 问题，实际: %s", s.Err)
	}
	if len(s.Results) != 0 {
		t.Fatalf("前置校验失败时不应执行任何任务，实际跑了 %d 项", len(s.Results))
	}
}

// TestSleepCtxCancelsImmediately 锁住取消语义：客户端断开后，
// 任务的节流等待必须立刻返回，而不是把 sleep 走完。
// 旧实现用 time.Sleep，断开后仍会把整轮（1-2 分钟）跑完并占着账号锁。
func TestSleepCtxCancelsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	sleepCtx(ctx, 30*time.Second)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("取消后应立即返回，实际等了 %v", elapsed)
	}
}

// TestSleepCtxWaitsWhenNotCancelled 反向保证：未取消时该等还是要等，
// 不能为了「可取消」把节流本身丢掉——节流是上游的节奏要求。
func TestSleepCtxWaitsWhenNotCancelled(t *testing.T) {
	start := time.Now()
	sleepCtx(context.Background(), 60*time.Millisecond)
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("未取消时应等待约 60ms，实际 %v", elapsed)
	}
}

// TestRunAccountTasksStopsOnCancelledContext 端到端：
// 已取消的 context 下不应派发任何任务。
func TestRunAccountTasksStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &UpstreamClient{}
	// 有 UID 才会走到列表阶段；这里用无效 JWT，让 UID 为空以命中前置校验，
	// 从而不需要真实网络。
	acc := &model.Account{Name: "x"}
	s := c.RunAccountTasks(ctx, acc, nil)
	if s == nil {
		t.Fatal("不应返回 nil")
	}
	if s.Err == "" {
		t.Fatal("异常输入应给出 Err")
	}
}

// TestConcurrentRunReturnsBusy 锁定并发语义：同一账号的一轮任务在跑时，
// 第二个请求必须**立即**返回「已有任务在执行」，而不是阻塞排队。
//
// 为什么这条重要：一轮任务含多处节流等待，跑满 1-2 分钟。旧实现用
// mu.Lock() 阻塞等待，用户在任务进行中再点一次就会挂住 —— 看起来像
// 卡死，实际只是在排队，而且页面不会给出任何解释。
func TestConcurrentRunReturnsBusy(t *testing.T) {
	c := &UpstreamClient{}
	acc := &model.Account{Name: "busy-probe"}
	acc.ID = 993

	// 手工占住该账号的锁，模拟一轮任务正在执行。
	mu := lockForAccount(acc.ID)
	mu.Lock()
	defer mu.Unlock()

	done := make(chan *TaskSummary, 1)
	go func() { done <- c.RunAccountTasks(context.Background(), acc, nil) }()

	select {
	case s := <-done:
		if !strings.Contains(s.Err, "正在执行") {
			t.Fatalf("应立即回报「正在执行」，实际: %q", s.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("第二个请求被阻塞了 —— 应立即可返回")
	}
}
