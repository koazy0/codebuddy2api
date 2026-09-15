package service

import (
	"context"
	"testing"
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
