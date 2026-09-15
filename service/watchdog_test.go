package service

import (
	"context"
	"testing"
	"time"

	"codebuddy-gateway/model"
)

// TestWatchdogRoundSkippedWhenRunning 锁住并发语义：
// 上一轮没跑完时本轮必须回报 Skipped，而不是静默返回一个全 0 结构。
//
// 为什么这条重要：面板靠 skipped 区分「真的没东西可做」和
// 「本轮根本没执行」。旧实现直接 return，调用方无法分辨，
// 面板只能显示「完成：检查 0 个账号」——那是误导。
func TestWatchdogRoundSkippedWhenRunning(t *testing.T) {
	w := &Watchdog{}
	// 手工占住 running 标志，模拟上一轮仍在执行。
	if !w.running.CompareAndSwap(false, true) {
		t.Fatal("初始状态应为未运行")
	}
	round := w.RunOnce(context.Background())
	if !round.Skipped {
		t.Fatal("上一轮未结束时应回报 Skipped")
	}
	if round.Checked != 0 || round.Recovered != 0 || round.Disabled != 0 {
		t.Fatalf("跳过的轮次不应带统计数字: %+v", round)
	}
	w.running.Store(false)
}

// TestWatchdogRoundFieldsExported 固定对外字段名。
// 前端按 round.checked / recovered / disabled / skipped 读取，
// 改字段名会让面板静默显示成 0。
func TestWatchdogRoundFieldsExported(t *testing.T) {
	round := WatchdogRound{Checked: 3, Recovered: 1, Disabled: 2}
	if round.Checked != 3 || round.Recovered != 1 || round.Disabled != 2 {
		t.Fatalf("字段语义被改动: %+v", round)
	}
	if round.Skipped {
		t.Fatal("默认不应标记为跳过")
	}
}

// TestNextCreditRecoveryTime 额度恢复检查点必须是「下一个 04:00」。
// 用固定时间点断言，避免依赖运行时刻。
func TestNextCreditRecoveryTime(t *testing.T) {
	loc := time.Local
	cases := []struct {
		now  time.Time
		want time.Time
	}{
		// 凌晨 2 点 → 当天 04:00
		{time.Date(2026, 9, 15, 2, 0, 0, 0, loc), time.Date(2026, 9, 15, 4, 0, 0, 0, loc)},
		// 上午 10 点 → 次日 04:00
		{time.Date(2026, 9, 15, 10, 0, 0, 0, loc), time.Date(2026, 9, 16, 4, 0, 0, 0, loc)},
		// 恰好 04:00 → 次日（不能用已过或正好的时点）
		{time.Date(2026, 9, 15, 4, 0, 0, 0, loc), time.Date(2026, 9, 16, 4, 0, 0, 0, loc)},
		// 深夜 23:30 → 次日 04:00
		{time.Date(2026, 9, 15, 23, 30, 0, 0, loc), time.Date(2026, 9, 16, 4, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		got := nextCreditRecoveryTime(c.now)
		if !got.Equal(c.want) {
			t.Errorf("nextCreditRecoveryTime(%v)=%v want %v", c.now, got, c.want)
		}
		if !got.After(c.now) {
			t.Errorf("恢复时点必须晚于当前时间: now=%v got=%v", c.now, got)
		}
	}
}

// TestCreditRemainOf 额度是月度 + 一次性之和，两处判定都读它。
func TestCreditRemainOf(t *testing.T) {
	acc := model.Account{MonthlyCreditRemain: 300, OnetimeCreditRemain: 50}
	if got := creditRemainOf(acc); got != 350 {
		t.Fatalf("creditRemainOf=%v want 350", got)
	}
	// 只有一次性额度也算有额度
	acc2 := model.Account{MonthlyCreditRemain: 0, OnetimeCreditRemain: 1}
	if got := creditRemainOf(acc2); got != 1 {
		t.Fatalf("一次性额度应计入: %v", got)
	}
}

// TestAccountHasCreditSemantics 锁定「未同步 ≠ 无额度」这条容易踩的边界。
// 把未同步判成无额度会让新导入账号一上来就被跳过。
func TestAccountHasCreditSemantics(t *testing.T) {
	synced := time.Now()
	// 未同步过 → 放行（不能误判成没额度）
	if !accountHasCredit(model.Account{CreditSyncedAt: nil}) {
		t.Fatal("未同步过账单的账号应视为可用，否则新号会被永久跳过")
	}
	// 已同步且为 0 → 无额度
	if accountHasCredit(model.Account{CreditSyncedAt: &synced}) {
		t.Fatal("已同步且额度为 0 应判为无额度")
	}
	// 已同步且有额度 → 有额度
	if !accountHasCredit(model.Account{CreditSyncedAt: &synced, MonthlyCreditRemain: 10}) {
		t.Fatal("有额度应判为可用")
	}
}

// TestReconcileWritesAndSyncsMemory 锁定一个靠实测才抓到的 bug：
// reconcileCreditState 写库后必须同步内存副本。
//
// 原因：调用方在它之后还会执行 model.UpdateAccount(&acc)（GORM Save，
// 整行写回）。若这里不同步，那次写入会拿内存里的旧状态把刚落库的结果
// 覆盖掉。线上表现是「日志说已冷却、库里仍是 enabled」，
// 且 last_error 也是空的——正是被整行覆盖的痕迹。
func TestReconcileWritesAndSyncsMemory(t *testing.T) {
	defer setupRotatorDB(t)()
	// 有同步过额度、但额度为 0 → 应转入冷却
	acc := addRotatorAccount(t, "zero", "jwt-zero", 0, true, 0)
	w := &Watchdog{}

	got := w.reconcileCreditState(acc)
	if got != creditStateNowCooling {
		t.Fatalf("零额度账号应转入冷却，实际 %v", got)
	}
	// 内存状态必须同步——否则后续整行写回会覆盖掉落库结果
	if acc.Status != model.AccountStatusCooldown {
		t.Fatalf("内存状态未同步: %s", acc.Status)
	}
	if acc.CooldownUntil == nil {
		t.Fatal("内存里的冷却时间未设置")
	}
	if acc.LastError != "credit exhausted" {
		t.Fatalf("内存里的 last_error 未同步: %q", acc.LastError)
	}

	// 数据库必须真的写进去了
	stored, err := model.GetAccountByID(acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.AccountStatusCooldown {
		t.Fatalf("库里的状态不对: %s", stored.Status)
	}
	if stored.LastError != "credit exhausted" {
		t.Fatalf("库里的 last_error 不对: %q", stored.LastError)
	}
}

// TestReconcileRestoresAccountWithCredit 有额度就把账号从冷却里捞回来。
// 这是「任务赚到积分后自动恢复」的核心路径。
func TestReconcileRestoresAccountWithCredit(t *testing.T) {
	defer setupRotatorDB(t)()
	acc := addRotatorAccount(t, "rich", "jwt-rich", 500, true, 0)
	// 人为置成冷却
	acc.Status = model.AccountStatusCooldown
	if err := model.UpdateAccount(acc); err != nil {
		t.Fatal(err)
	}
	w := &Watchdog{}

	got := w.reconcileCreditState(acc)
	if got != creditStateNowEnabled {
		t.Fatalf("有额度应从冷却恢复，实际 %v", got)
	}
	if acc.Status != model.AccountStatusEnabled || acc.CooldownUntil != nil {
		t.Fatalf("内存状态未同步: status=%s until=%v", acc.Status, acc.CooldownUntil)
	}
	stored, _ := model.GetAccountByID(acc.ID)
	if stored.Status != model.AccountStatusEnabled {
		t.Fatalf("库里状态不对: %s", stored.Status)
	}
}

// TestReconcileNoopWhenAlreadyCorrect 状态已经正确时不写库、不报变更，
// 避免统计虚增。
func TestReconcileNoopWhenAlreadyCorrect(t *testing.T) {
	defer setupRotatorDB(t)()
	w := &Watchdog{}

	// 有额度 + 已启用 → 无变更
	enabled := addRotatorAccount(t, "ok", "jwt-ok", 100, true, 0)
	if got := w.reconcileCreditState(enabled); got != creditStateUnchanged {
		t.Fatalf("状态已正确时不应报变更，实际 %v", got)
	}

	// 零额度 + 冷却中且有未来冷却时间 → 无变更
	cooling := addRotatorAccount(t, "cooling", "jwt-cooling", 0, true, 0)
	cooling.Status = model.AccountStatusCooldown
	future := time.Now().Add(time.Hour)
	cooling.CooldownUntil = &future
	if err := model.UpdateAccount(cooling); err != nil {
		t.Fatal(err)
	}
	if got := w.reconcileCreditState(cooling); got != creditStateUnchanged {
		t.Fatalf("冷却未到期时不应重复写，实际 %v", got)
	}
}

// TestReconcileRenewsExpiredCooldown 冷却已过期但额度仍为 0 时必须续期。
// 否则账号停在「冷却已过期」的中间态：轮询的查询会把过期冷却视为可用，
// 于是又被选中、又失败。
func TestReconcileRenewsExpiredCooldown(t *testing.T) {
	defer setupRotatorDB(t)()
	acc := addRotatorAccount(t, "stale", "jwt-stale", 0, true, 0)
	acc.Status = model.AccountStatusCooldown
	past := time.Now().Add(-time.Hour)
	acc.CooldownUntil = &past
	if err := model.UpdateAccount(acc); err != nil {
		t.Fatal(err)
	}
	w := &Watchdog{}

	if got := w.reconcileCreditState(acc); got != creditStateNowCooling {
		t.Fatalf("过期冷却应续期，实际 %v", got)
	}
	if acc.CooldownUntil == nil || !acc.CooldownUntil.After(time.Now()) {
		t.Fatal("冷却时间应被续到未来")
	}
}

// TestReconcileSkipsManualDisable 人工停用的账号不参与额度驱动。
// 这是硬边界：自动逻辑不得覆盖人的决定。
func TestReconcileSkipsManualDisable(t *testing.T) {
	w := &Watchdog{}
	synced := time.Now()
	acc := &model.Account{
		Status:           model.AccountStatusDisabled,
		ManuallyDisabled: true,
		CreditSyncedAt:   &synced,
	}
	acc.ID = 1
	if got := w.reconcileCreditState(acc); got != creditStateUnchanged {
		t.Fatalf("人工停用的账号不应被改动，实际 %v", got)
	}
	if acc.Status != model.AccountStatusDisabled {
		t.Fatalf("人工停用的状态被改了: %s", acc.Status)
	}
}

// TestCreditStateActionValues 固定枚举语义：unchanged 不能与两个变更混淆，
// 否则统计会虚增或漏报。
func TestCreditStateActionValues(t *testing.T) {
	if creditStateUnchanged == creditStateNowEnabled || creditStateUnchanged == creditStateNowCooling {
		t.Fatal("unchanged 必须与两个变更值不同")
	}
	if creditStateNowEnabled == creditStateNowCooling {
		t.Fatal("两个变更值必须不同")
	}
}
