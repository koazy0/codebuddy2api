package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"codebuddy-gateway/global"
	"codebuddy-gateway/model"

	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// 任务中心调度
//
// 一次完整执行分四阶段，顺序不可颠倒：
//  1. accept —— 批量报名。**这是前提**：未报名时 progress.target 恒为 0，
//     此时上报事件不计数（实测确认），所以必须先报名再跑行为。
//  2. run    —— 按依赖顺序执行各任务的事件链。
//  3. settle —— 等待异步计分落定。进度不是同步更新的，上报完立刻查
//     通常还是旧值，需要给服务端一点时间。
//  4. claim  —— 对已达标任务自动领奖。
//
// 全程幂等：已 claimed 的任务会被跳过，重复点「一键完成」不会重复加分。
// ---------------------------------------------------------------------------

// settleWait 计分落定的等待时长。上游进度是异步聚合的，实测 3 秒足够。
const settleWait = 3 * time.Second

// TaskRunResult 单个任务的执行结果。
type TaskRunResult struct {
	Code    string `json:"code"`
	Desc    string `json:"desc"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	// Skipped 表示该任务已领取或无对应动作，未实际执行。
	Skipped bool `json:"skipped,omitempty"`
}

// TaskSummary 一次任务中心执行的整体结果。
type TaskSummary struct {
	AccountID uint            `json:"account_id"`
	Account   string          `json:"account,omitempty"`
	Results   []TaskRunResult `json:"results"`
	// Claimed 本次成功领取的任务码。
	Claimed []string `json:"claimed,omitempty"`
	// CreditGained / EnergyGained 本次到账的奖励汇总。
	CreditGained int64 `json:"credit_gained"`
	EnergyGained int64 `json:"energy_gained"`
	// Before / After 执行前后的未领取奖励估算，便于直观看到收益。
	Before int64  `json:"before_pending"`
	After  int64  `json:"after_pending"`
	Err    string `json:"error,omitempty"`
}

// taskLocks 按账号串行化任务执行：同一账号的任务动作不能并发，
// 否则两次执行会交叉上报事件、互相干扰进度判定。
var taskLocks sync.Map // map[uint]*sync.Mutex

func lockForAccount(id uint) *sync.Mutex {
	v, _ := taskLocks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// RunAccountTasks 对单个账号执行完整的任务中心流程。
//
// only 非空时只跑指定任务码（用于面板里点单项）；为空则跑全部可自动化任务。
func (c *UpstreamClient) RunAccountTasks(ctx context.Context, acc *model.Account, only []string) *TaskSummary {
	summary := &TaskSummary{
		AccountID: acc.ID,
		Account:   accountDisplayName(acc),
		Results:   make([]TaskRunResult, 0, len(taskRunners)),
	}

	// 用 TryLock 而不是 Lock：一轮任务含多处节流等待，跑满要 1-2 分钟。
	// 若这里阻塞等待，用户在任务进行中再点一次就会「挂住不动」——
	// 看起来像卡死，实际只是在排队。直接告诉他原因更有用。
	mu := lockForAccount(acc.ID)
	if !mu.TryLock() {
		summary.Err = "该账号已有任务正在执行，请等待当前这轮结束"
		return summary
	}
	defer mu.Unlock()

	// 前置校验：没有 userId 就不要往下跑。
	//
	// 行为事件必须带 userId，缺失时上游返回 200 但**静默丢弃**它，
	// 任务进度一动不动。那种情况下整套流程会"全程成功、结果为零"，
	// 是最难排查的失败形态。这里提前拦下并说清原因。
	if acc.UID() == "" {
		summary.Err = "该账号缺少 userId（JWT 里没有 sub，导入数据里也没有 uid），" +
			"行为事件会被上游静默丢弃，任务无法计分。请重新导入带完整凭据的账号。"
		return summary
	}

	before, err := c.GrowthListTasks(ctx, acc)
	if err != nil {
		summary.Err = "拉取任务列表失败: " + err.Error()
		return summary
	}
	summary.Before = pendingCredit(before)

	// 阶段 1：批量报名。
	if err := c.acceptPending(ctx, acc, before); err != nil {
		global.CORE_LOG.Warn("task accept phase failed", zap.Uint("account_id", acc.ID), zap.Error(err))
	}

	// 阶段 2：按序执行各任务的动作。
	runners := selectRunners(only)
	for _, r := range runners {
		// 客户端已断开就停止派发后续任务。
		// 底层请求与节流等待都监听 ctx，但循环本身要主动查一次——
		// 否则一个任务跑完后仍会继续派发下一个，把整轮空跑完。
		if err := ctx.Err(); err != nil {
			summary.Err = "任务已中止（客户端断开）"
			break
		}
		// 已领取或未解锁的任务直接跳过，避免无用调用。
		// Locked 是上游给的「还不该做」标记（前置任务没完成等），
		// 硬跑只会被拒，白费一次请求。
		if t := findTask(before, r.Code); t != nil {
			if t.Claimed {
				summary.Results = append(summary.Results, TaskRunResult{
					Code: r.Code, Desc: r.Desc, Skipped: true, Message: "已领取，跳过",
				})
				continue
			}
			if t.Locked {
				summary.Results = append(summary.Results, TaskRunResult{
					Code: r.Code, Desc: r.Desc, Skipped: true, Message: "任务未解锁，跳过",
				})
				continue
			}
		}
		msg, err := r.run(ctx, c, acc)
		res := TaskRunResult{Code: r.Code, Desc: r.Desc, Message: msg}
		if err != nil {
			res.Error = err.Error()
			global.CORE_LOG.Warn("task run failed",
				zap.Uint("account_id", acc.ID), zap.String("code", r.Code), zap.Error(err))
		}
		summary.Results = append(summary.Results, res)
	}

	// 阶段 3：等计分落定。
	sleepCtx(ctx, settleWait)
	if err := ctx.Err(); err != nil {
		summary.Err = "任务已中止（客户端断开）"
		return summary
	}

	// 阶段 4：领取已达标的任务。
	after, err := c.GrowthListTasks(ctx, acc)
	if err != nil {
		summary.Err = "领取阶段拉取任务列表失败: " + err.Error()
		return summary
	}
	for _, t := range after {
		if !t.Claimable {
			continue
		}
		// 指定了 only 时只领这些，避免误领用户没点的任务。
		if len(only) > 0 && !containsFold(only, t.TaskCode) {
			continue
		}
		credit, energy, err := c.GrowthClaimReward(ctx, acc, t.TaskCode)
		if err != nil {
			global.CORE_LOG.Warn("claim failed",
				zap.Uint("account_id", acc.ID), zap.String("code", t.TaskCode), zap.Error(err))
			continue
		}
		if credit > 0 || energy > 0 {
			summary.Claimed = append(summary.Claimed, t.TaskCode)
			summary.CreditGained += credit
			summary.EnergyGained += energy
		}
		sleepCtx(ctx, 400*time.Millisecond)
	}

	// 收尾：再查一次，给出剩余未领取奖励。
	if final, err := c.GrowthListTasks(ctx, acc); err == nil {
		summary.After = pendingCredit(final)
	}
	return summary
}

// acceptPending 批量报名尚未接受的任务。
func (c *UpstreamClient) acceptPending(ctx context.Context, acc *model.Account, tasks []GrowthTask) error {
	var codes []string
	for _, t := range tasks {
		if t.Claimed || t.Locked {
			continue
		}
		if t.AcceptStatus == "accepted" || t.AcceptStatus == "completed" {
			continue
		}
		codes = append(codes, t.TaskCode)
	}
	if len(codes) == 0 {
		return nil
	}
	return c.GrowthAcceptTasks(ctx, acc, codes)
}

// selectRunners 按 only 过滤执行器；only 为空时返回全部（顺序即依赖顺序）。
func selectRunners(only []string) []taskRunner {
	if len(only) == 0 {
		out := make([]taskRunner, len(taskRunners))
		copy(out, taskRunners)
		return out
	}
	var out []taskRunner
	for _, code := range only {
		if r := runnerFor(code); r != nil {
			out = append(out, *r)
		}
	}
	return out
}

// pendingCredit 汇总未领取任务的奖励总额。
func pendingCredit(tasks []GrowthTask) int64 {
	var total int64
	for _, t := range tasks {
		if t.Claimed {
			continue
		}
		total += t.Credit
	}
	return total
}

func findTask(tasks []GrowthTask, code string) *GrowthTask {
	for i := range tasks {
		if strings.EqualFold(tasks[i].TaskCode, code) {
			return &tasks[i]
		}
	}
	return nil
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func accountDisplayName(acc *model.Account) string {
	if acc.Name != "" {
		return acc.Name
	}
	if acc.Username != "" {
		return acc.Username
	}
	return fmt.Sprintf("#%d", acc.ID)
}

// TaskCatalog 返回可自动化任务清单。
// 目前没有前端入口，保留作排障用：可一览哪些任务码能一键完成、哪些是尝试型。
func TaskCatalog() []map[string]any {
	out := make([]map[string]any, 0, len(taskRunners))
	for _, r := range taskRunners {
		out = append(out, map[string]any{
			"code": r.Code, "desc": r.Desc, "attempt": r.Attempt,
		})
	}
	return out
}

// SortTasksByReward 按奖励降序排列任务，便于面板把「值钱的」排前面。
func SortTasksByReward(tasks []GrowthTask) {
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Claimed != tasks[j].Claimed {
			return !tasks[i].Claimed
		}
		return tasks[i].Credit > tasks[j].Credit
	})
}
