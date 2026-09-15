package service

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// TestBatchStartRejectsConcurrentRun 锁定并发语义：上一批在跑时，
// 再次启动必须被拒而不是排队。
//
// 为什么重要：两个批次同时跑会让同一账号被两轮抢着上报事件，
// 进度判定会互相干扰；而且上游压力翻倍，更容易触发限流。
func TestBatchStartRejectsConcurrentRun(t *testing.T) {
	b := &batchRunner{}
	// 手工置一个「正在跑」的状态，避免依赖真实网络。
	b.mu.Lock()
	b.state = &BatchState{ID: "x", Running: true, Total: 1, Results: []BatchAccountResult{{Status: "running"}}}
	b.mu.Unlock()

	if _, err := b.Start(nil, 1); err == nil {
		t.Fatal("上一批在跑时应拒绝启动")
	} else if !strings.Contains(err.Error(), "仍在执行") {
		t.Fatalf("错误信息应说明原因，实际: %v", err)
	}
}

// TestBatchSnapshotIsCopy 锁住快照语义：Start 返回的必须是副本。
// 若直接把内部 state 交出去，后台 goroutine 改写它时会让调用方
// 读到半更新的数据（数据竞争 + 进度显示错乱）。
func TestBatchSnapshotIsCopy(t *testing.T) {
	b := &batchRunner{}
	b.mu.Lock()
	b.state = &BatchState{
		ID:      "s",
		Total:   2,
		Results: []BatchAccountResult{{AccountID: 1}, {AccountID: 2}},
	}
	snap := b.snapshotLocked()
	b.state.Results[0].AccountID = 999 // 模拟后台改写
	b.mu.Unlock()

	if snap.Results[0].AccountID != 1 {
		t.Fatalf("快照应独立于内部状态，实际被改写为 %d", snap.Results[0].AccountID)
	}
}

// TestBatchSetResultGuardsBounds 越界写入不应 panic——
// 账号列表在启动后理论上不变，但防御性检查不能少。
func TestBatchSetResultGuardsBounds(t *testing.T) {
	b := &batchRunner{}
	b.mu.Lock()
	b.state = &BatchState{Results: []BatchAccountResult{{Status: "pending"}}}
	b.mu.Unlock()

	b.setResult(5, func(r *BatchAccountResult) { r.Status = "done" })
	b.setResult(-1, func(r *BatchAccountResult) { r.Status = "done" })

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state.Results[0].Status != "pending" {
		t.Fatalf("越界写入不应影响已有结果: %s", b.state.Results[0].Status)
	}
}

// TestIsSkipReason 「账号忙」是暂时性跳过，「缺 userId」是配置性失败。
// 两者混为一谈会让用户对配置问题反复重试。
func TestIsSkipReason(t *testing.T) {
	skip := []string{"该账号已有任务正在执行，请等待当前这轮结束"}
	for _, s := range skip {
		if !isSkipReason(s) {
			t.Errorf("应判为跳过: %q", s)
		}
	}
	fail := []string{
		"该账号缺少 userId（JWT 里没有 sub），行为事件会被上游静默丢弃",
		"拉取任务列表失败: http 500",
		"",
	}
	for _, s := range fail {
		if isSkipReason(s) {
			t.Errorf("不应判为跳过: %q", s)
		}
	}
}

// TestBatchStoresSnapshotUnderLock 验证状态读写全程持锁：
// 多个账号并发写结果，-race 下不应报竞争。
func TestBatchStoresSnapshotUnderLock(t *testing.T) {
	b := &batchRunner{}
	b.mu.Lock()
	b.state = &BatchState{Results: make([]BatchAccountResult, 8)}
	b.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			b.setResult(idx, func(r *BatchAccountResult) { r.Status = "done" })
			_ = b.Status()
		}(i)
	}
	wg.Wait()

	st := b.Status()
	for i, r := range st.Results {
		if r.Status != "done" {
			t.Fatalf("第 %d 项未写入: %s", i, r.Status)
		}
	}
}

// TestBatchAggregatesTotals 合计值要正确累加，面板靠它显示总收益。
func TestBatchAggregatesTotals(t *testing.T) {
	b := &batchRunner{}
	b.mu.Lock()
	b.state = &BatchState{Results: []BatchAccountResult{{}, {}}}
	b.mu.Unlock()

	// 模拟两个账号各自累加
	for _, add := range []struct{ c, e int64 }{{100, 5}, {300, 8}} {
		b.mu.Lock()
		b.state.CreditGained += add.c
		b.state.EnergyGained += add.e
		b.mu.Unlock()
	}
	st := b.Status()
	if st.CreditGained != 400 || st.EnergyGained != 13 {
		t.Fatalf("合计错误: credit=%d energy=%d", st.CreditGained, st.EnergyGained)
	}
}

// TestBatchNoneStatus 从未启动过时必须能区分「没有批次」和「批次为空」。
func TestBatchNoneStatus(t *testing.T) {
	b := &batchRunner{}
	if st := b.Status(); st != nil {
		t.Fatalf("未启动过应返回 nil，实际 %+v", st)
	}
}

// TestBatchConcurrencyClamp 并发上限要生效，否则用户传个 999
// 会把所有账号一起打向上游。
func TestBatchConcurrencyClamp(t *testing.T) {
	if batchMaxConcurrency <= 0 || batchMaxConcurrency > 32 {
		t.Fatalf("并发上限取值不合理: %d", batchMaxConcurrency)
	}
	if batchDefaultConcurrency <= 0 || batchDefaultConcurrency > batchMaxConcurrency {
		t.Fatalf("默认并发应与上限自洽: %d vs %d", batchDefaultConcurrency, batchMaxConcurrency)
	}
}

var _ = time.Second
