package service

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"codebuddy-gateway/global"
	"codebuddy-gateway/model"

	"go.uber.org/zap"
)

type Watchdog struct {
	client    *UpstreamClient
	refresher *Refresher
	running   atomic.Bool
}

func NewWatchdog(client *UpstreamClient, refresher *Refresher) *Watchdog {
	return &Watchdog{client: client, refresher: refresher}
}

// WatchdogRound 一轮看门狗的统计结果。
//
// 存在的意义：调用方（面板 / 排程）需要知道这一轮到底做了什么。
// 尤其 Skipped 与 Disabled：前者说明上一轮还没跑完、这轮被跳过，
// 后者说明有账号刚被判死——只回一句「跑完了」会把这些信息全丢掉。
type WatchdogRound struct {
	// Skipped 为真表示本轮因上一轮未结束而未执行。
	Skipped bool `json:"skipped,omitempty"`
	Checked int  `json:"checked"`
	// Recovered 续期成功 + 冷却到期恢复的账号数。
	Recovered int `json:"recovered"`
	// Disabled 本轮被判定异常的账号数。
	Disabled int `json:"disabled"`
}

// RunOnce 跑一轮看门狗并返回统计。
func (w *Watchdog) RunOnce(ctx context.Context) WatchdogRound {
	if !w.running.CompareAndSwap(false, true) {
		return WatchdogRound{Skipped: true}
	}
	defer w.running.Store(false)

	list, err := model.ListAccounts()
	if err != nil {
		global.CORE_LOG.Error("watchdog list accounts failed", zap.Error(err))
		return WatchdogRound{}
	}

	threshold := time.Duration(global.CORE_CONFIG.Refresh.Threshold()) * 24 * time.Hour
	checked := 0
	recovered := 0
	disabled := 0

	for i := range list {
		acc := list[i]
		checked++
		now := time.Now()

		// 人工停用的账号一律不碰：状态归人管，自动逻辑不越权。
		// 这是硬边界——把人手动停用的账号自动启回来会覆盖人的明确意图。
		if acc.ManuallyDisabled {
			continue
		}

		// 冷却到期只解除「时间锁」，是否真正启用交给下面的额度判定。
		// 旧实现直接在这里改成 enabled，会在额度仍为 0 时把账号放回可用池，
		// 轮询再把它挑出来、调用失败、又进冷却——来回抖动。
		if acc.Status == model.AccountStatusCooldown && acc.CooldownUntil != nil && now.After(*acc.CooldownUntil) {
			acc.CooldownUntil = nil
			acc.FailCount = 0
			_ = model.UpdateAccount(&acc)
			global.CORE_LOG.Info("watchdog cooldown expired, credit re-check pending",
				zap.Uint("account_id", acc.ID), zap.String("name", acc.Name))
		}

		if acc.Status == model.AccountStatusDisabled {
			continue
		}
		if acc.JWT == "" {
			continue
		}

		if global.CORE_CONFIG.Refresh.Enabled && ShouldRefresh(acc.JWT, threshold) {
			if err := w.refresher.RefreshAccount(ctx, &acc); err != nil {
				global.CORE_LOG.Warn("watchdog refresh failed", zap.Uint("account_id", acc.ID), zap.Error(err))
				w.markFail(&acc, "refresh failed: "+err.Error())
				disabled++
				continue
			}
			recovered++
		}

		_ = model.RefreshMonthlyCycleIfNeeded(acc.ID)

		// 额度同步与状态判定。
		//
		// 两件事刻意分开：
		//   同步 —— 受 sync-credit 开关控制（会打上游接口，可关掉省流量）；
		//   判定 —— 总是执行，用库里已有的额度数据决定状态。
		// 混在一起的后果是：关掉同步就等于关掉状态收敛，账号会长期停在
		// 错误状态上（无额度的留在可用池、有额度的留在冷却里）。
		if global.CORE_CONFIG.Watchdog.SyncCredit {
			if err := w.SyncAccountCredit(ctx, &acc); err != nil {
				global.CORE_LOG.Warn("watchdog credit sync failed", zap.Uint("account_id", acc.ID), zap.Error(err))
			}
			latest, err := model.GetAccountByID(acc.ID)
			if err == nil && latest != nil {
				acc = *latest
			}
		}

		// 状态收敛：额度是状态的唯一依据。
		//   额度为 0 → 转入冷却，不计入轮询；
		//   额度 > 0 → 立即恢复启用。
		// 用库里已有的额度数据判断，因此不需要联网也能收敛。
		if !acc.ManuallyDisabled && acc.CreditSyncedAt != nil {
			switch w.reconcileCreditState(&acc) {
			case creditStateNowCooling:
				disabled++
			case creditStateNowEnabled:
				recovered++
			}
		}

		if acc.Status != model.AccountStatusEnabled {
			continue
		}
		if !global.CORE_CONFIG.Watchdog.HealthCheck {
			continue
		}

		checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := w.client.CheckAccount(checkCtx, &acc)
		cancel()
		if err != nil {
			global.CORE_LOG.Warn("watchdog health check failed", zap.Uint("account_id", acc.ID), zap.Error(err))
			w.markFail(&acc, "health check failed: "+err.Error())
			disabled++
			continue
		}
		now = time.Now()
		acc.LastCheckedAt = &now
		acc.FailCount = 0
		acc.LastError = ""
		_ = model.UpdateAccount(&acc)
	}

	global.CORE_LOG.Info("watchdog round done",
		zap.Int("checked", checked),
		zap.Int("recovered", recovered),
		zap.Int("disabled", disabled),
	)
	return WatchdogRound{Checked: checked, Recovered: recovered, Disabled: disabled}
}

func (w *Watchdog) SyncAccountCredit(ctx context.Context, acc *model.Account) error {
	if acc == nil {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	snap, err := w.client.FetchAccountCredit(checkCtx, acc)
	if err == nil && snap != nil {
		return model.SaveCreditSnapshot(acc.ID, *snap)
	}

	notify, notifyErr := w.client.CheckDosage(checkCtx, acc)
	if notifyErr == nil && notify != nil {
		msg := firstNonEmpty(notify.Zh, notify.En)
		_ = model.SaveDosageNotify(acc.ID, notify.Code, msg)
		if notify.Code != 0 {
			return fmt.Errorf("dosage notify code %d: %s", notify.Code, msg)
		}
		if err != nil {
			global.CORE_LOG.Warn("credit snapshot unavailable, dosage only",
				zap.Uint("account_id", acc.ID),
				zap.Error(err),
			)
		}
		return nil
	}
	if err != nil {
		return err
	}
	return notifyErr
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (w *Watchdog) markFail(acc *model.Account, msg string) {
	fail := acc.FailCount + 1
	status := model.AccountStatusEnabled
	var cooldown *time.Time
	if fail >= global.CORE_CONFIG.Watchdog.FailLimit() {
		status = model.AccountStatusCooldown
		until := time.Now().Add(time.Duration(global.CORE_CONFIG.Watchdog.Cooldown()) * time.Second)
		cooldown = &until
	}
	_ = model.MarkAccountFailure(acc.ID, msg, fail, status, cooldown)
}

// reconcileCreditState 按额度把账号调整到应有的状态。
//
// 这是额度驱动的唯一决策点，规则很直白：
//   - 有额度 → enabled（立即恢复，任务赚到积分后无需人工干预）
//   - 无额度 → cooldown，并把冷却时间设到次日 04:00
//
// 为什么无额度用 cooldown 而不是 disabled：
// disabled 语义上归属人工（见 Account.ManuallyDisabled），自动逻辑把账号
// 标成 disabled 会与人手动停用的混在一起，无法区分该不该自动恢复。
// cooldown 天然是「临时、可自动解除」的语义，正合适。
//
// 为什么冷却到次日 04:00：上游额度按自然日恢复，凌晨 4 点留了余量，
// 避免刚过零点就反复探测。中间若有任务赚到积分，下一轮看门狗会
// 立即把它捞回来——不必等这个时间点。
//
// 返回本轮实际发生的状态变更；没有变更时返回 creditStateUnchanged。
// 用枚举而不是布尔，是因为「恢复了」和「本来就是启用」对调用方是两回事——
// 混在一个 bool 里会让统计虚增。
func (w *Watchdog) reconcileCreditState(acc *model.Account) creditStateAction {
	if acc == nil {
		return creditStateUnchanged
	}
	// 人工停用的账号不参与额度驱动。
	if acc.ManuallyDisabled {
		return creditStateUnchanged
	}

	hasCredit := accountHasCredit(*acc)

	if hasCredit {
		if acc.Status == model.AccountStatusEnabled {
			return creditStateUnchanged
		}
		prev := acc.Status
		if err := model.MarkAccountEnabled(acc.ID); err != nil {
			global.CORE_LOG.Warn("watchdog re-enable failed",
				zap.Uint("account_id", acc.ID), zap.Error(err))
			return creditStateUnchanged
		}
		global.CORE_LOG.Info("watchdog credit available, re-enabled",
			zap.Uint("account_id", acc.ID),
			zap.String("name", acc.Name),
			zap.String("from", prev))
		return creditStateNowEnabled
	}

	// 无额度：已经处于冷却就不用重复写。
	if acc.Status == model.AccountStatusCooldown {
		return creditStateUnchanged
	}
	until := nextCreditRecoveryTime(time.Now())
	if err := model.MarkAccountCreditExhausted(acc.ID, until); err != nil {
		global.CORE_LOG.Warn("watchdog credit-exhausted update failed",
			zap.Uint("account_id", acc.ID), zap.Error(err))
		return creditStateUnchanged
	}
	global.CORE_LOG.Info("watchdog credit exhausted, cooling down",
		zap.Uint("account_id", acc.ID),
		zap.String("name", acc.Name),
		zap.Time("until", until))
	return creditStateNowCooling
}

// creditStateAction 是额度收敛这一轮实际做出的状态变更。
type creditStateAction int

const (
	// creditStateUnchanged 账号状态已经正确，无需改动。
	creditStateUnchanged creditStateAction = iota
	// creditStateNowEnabled 从其它状态恢复为启用。
	creditStateNowEnabled
	// creditStateNowCooling 因额度耗尽转入冷却。
	creditStateNowCooling
)

// nextCreditRecoveryTime 返回下一次额度恢复的检查点（本地时区次日 04:00）。
func nextCreditRecoveryTime(now time.Time) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), 4, 0, 0, 0, now.Location())
	if !t.After(now) {
		t = t.Add(24 * time.Hour)
	}
	return t
}
