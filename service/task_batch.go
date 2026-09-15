package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"codebuddy-gateway/global"
	"codebuddy-gateway/model"

	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// 批量任务中心
//
// 为什么是异步队列而不是同步接口：
// 单个账号跑完约 1-2 分钟（各处节流间隔合计）。若把 N 个账号串在同一个
// HTTP 请求里，10 个账号就要 10-20 分钟——面板后面若有反向代理或隧道
// （cloudflared 之类），这种长连接必然被掐断，用户只看到超时。
//
// 所以接口只负责「启动」并立刻返回，实际执行在后台 goroutine 里跑，
// 前端轮询进度。这与任务本身的语义也更契合：计分是异步的。
// ---------------------------------------------------------------------------

const (
	// batchDefaultConcurrency 默认并发账号数。
	// 不取更高：所有账号打的是同一个上游，并发过高容易被判定为异常流量，
	// 反而拖慢整体（触发限流后每步都要重试）。
	batchDefaultConcurrency = 3
	batchMaxConcurrency     = 8
)

// BatchAccountResult 批量执行中单个账号的结果。
type BatchAccountResult struct {
	AccountID uint   `json:"account_id"`
	Account   string `json:"account"`
	// Status 取值：pending / running / done / skipped / failed。
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	// Credit / Energy 该账号本次到账的奖励。
	Credit int64 `json:"credit"`
	Energy int64 `json:"energy"`
	// Claimed 该账号本次领取的任务码。
	Claimed []string `json:"claimed,omitempty"`
}

// BatchState 一次批量执行的快照。
type BatchState struct {
	ID         string               `json:"id"`
	Running    bool                 `json:"running"`
	StartedAt  time.Time            `json:"started_at"`
	FinishedAt *time.Time           `json:"finished_at,omitempty"`
	Total      int                  `json:"total"`
	Done       int                  `json:"done"`
	Concurrent int                  `json:"concurrency"`
	Results    []BatchAccountResult `json:"results"`
	// CreditGained / EnergyGained 全部账号合计到账。
	CreditGained int64 `json:"credit_gained"`
	EnergyGained int64 `json:"energy_gained"`
}

// batchRunner 批量执行器。面板同一时刻只需要一批，所以做成单例：
// 重复启动同一批只会互相干扰（同一账号被两轮抢着上报事件）。
type batchRunner struct {
	mu    sync.Mutex
	state *BatchState
}

var defaultBatch = &batchRunner{}

// Start 启动一批账号的任务执行，立刻返回状态快照。
// 已有批次在跑时返回错误——不排队，避免用户的两次点击叠成两倍上游压力。
func (b *batchRunner) Start(only []string, concurrency int) (*BatchState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state != nil && b.state.Running {
		return nil, fmt.Errorf("上一批任务仍在执行中，请等待完成后再启动")
	}

	list, err := model.ListAccounts()
	if err != nil {
		return nil, err
	}
	// 只排除已停用的账号。
	// 冷却中的账号**要**跑：冷却源于对话额度耗尽，而任务赚的正是积分，
	// 让它继续做任务反而有助于恢复，没有理由跳过。
	targets := make([]model.Account, 0, len(list))
	for _, acc := range list {
		if acc.Status == model.AccountStatusDisabled {
			continue
		}
		targets = append(targets, acc)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("没有可执行的账号（已全部停用）")
	}

	if concurrency <= 0 {
		concurrency = batchDefaultConcurrency
	}
	if concurrency > batchMaxConcurrency {
		concurrency = batchMaxConcurrency
	}

	results := make([]BatchAccountResult, len(targets))
	for i, acc := range targets {
		results[i] = BatchAccountResult{
			AccountID: acc.ID,
			Account:   accountDisplayName(&acc),
			Status:    "pending",
		}
	}
	b.state = &BatchState{
		ID:         fmt.Sprintf("batch-%d", time.Now().UnixNano()),
		Running:    true,
		StartedAt:  time.Now(),
		Total:      len(targets),
		Concurrent: concurrency,
		Results:    results,
	}
	// 复制一份快照返回：调用方拿到的是「启动瞬间」的状态，
	// 后台会继续改写 b.state，不能把它直接交出去。
	snapshot := b.snapshotLocked()

	go b.run(targets, only, concurrency)
	return snapshot, nil
}

// snapshotLocked 取当前状态副本（调用方须已持锁）。
func (b *batchRunner) snapshotLocked() *BatchState {
	if b.state == nil {
		return nil
	}
	cp := *b.state
	cp.Results = append([]BatchAccountResult(nil), b.state.Results...)
	return &cp
}

// Status 返回当前批次状态；从未启动过时返回 nil。
func (b *batchRunner) Status() *BatchState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked()
}

// run 后台执行全部账号。用 context.Background() 而不是请求的 ctx：
// 接口早已返回，任务要独立于那次 HTTP 请求继续跑完。
func (b *batchRunner) run(targets []model.Account, only []string, concurrency int) {
	defer func() {
		b.mu.Lock()
		if b.state != nil {
			now := time.Now()
			b.state.FinishedAt = &now
			b.state.Running = false
		}
		b.mu.Unlock()
	}()

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	client := DefaultClient

	for i := range targets {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b.runOne(client, idx, targets[idx], only)
		}(i)
	}
	wg.Wait()
}

// runOne 执行单个账号，并把结果写回状态。
func (b *batchRunner) runOne(client *UpstreamClient, idx int, acc model.Account, only []string) {
	b.setResult(idx, func(r *BatchAccountResult) { r.Status = "running" })

	// RunAccountTasks 内部用 per-account TryLock。这里不预判锁状态：
	// 如果该账号正被单账号接口操作，它会自己回报「已有任务在执行」，
	// 我们把那条信息如实透出即可，不必重复实现一遍。
	summary := client.RunAccountTasks(context.Background(), &acc, only)

	b.setResult(idx, func(r *BatchAccountResult) {
		if summary.Err != "" {
			// 缺 userId、账号忙都走这里。区分「跳过」与「失败」：
			// 前者重试可能有用，后者是配置问题，重试也一样。
			if isSkipReason(summary.Err) {
				r.Status = "skipped"
			} else {
				r.Status = "failed"
			}
			r.Message = summary.Err
			return
		}
		r.Status = "done"
		r.Credit = summary.CreditGained
		r.Energy = summary.EnergyGained
		r.Claimed = summary.Claimed
		r.Message = fmt.Sprintf("领取 %d 个任务，+%d 分 +%d 能",
			len(summary.Claimed), summary.CreditGained, summary.EnergyGained)
	})

	b.mu.Lock()
	if b.state != nil {
		b.state.Done++
		b.state.CreditGained += summary.CreditGained
		b.state.EnergyGained += summary.EnergyGained
	}
	b.mu.Unlock()

	global.CORE_LOG.Info("batch task account finished",
		zap.Uint("account_id", acc.ID),
		zap.String("status", summary.Err),
		zap.Int64("credit", summary.CreditGained))
}

// isSkipReason 判断错误属于「这轮跳过」而非「配置性失败」。
// 账号正忙是暂时的，下次跑就好；缺 userId 是配置问题，重试也一样。
func isSkipReason(msg string) bool {
	return strings.Contains(msg, "正在执行") || strings.Contains(msg, "已有任务")
}

// setResult 在锁保护下改写某个账号的结果。
func (b *batchRunner) setResult(idx int, fn func(*BatchAccountResult)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == nil || idx < 0 || idx >= len(b.state.Results) {
		return
	}
	fn(&b.state.Results[idx])
}

// ---------------------------------------------------------------------------
// 对外入口
// ---------------------------------------------------------------------------

// StartBatchTasks 启动批量任务执行（供 HTTP 层调用）。
func StartBatchTasks(only []string, concurrency int) (*BatchState, error) {
	return defaultBatch.Start(only, concurrency)
}

// BatchTasksStatus 返回当前批量执行状态；从未启动过时返回 nil。
func BatchTasksStatus() *BatchState {
	return defaultBatch.Status()
}
