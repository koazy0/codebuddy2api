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
