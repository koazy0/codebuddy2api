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

func (w *Watchdog) RunOnce(ctx context.Context) {
	if !w.running.CompareAndSwap(false, true) {
		return
	}
	defer w.running.Store(false)

	list, err := model.ListAccounts()
	if err != nil {
		global.CORE_LOG.Error("watchdog list accounts failed", zap.Error(err))
		return
	}

	threshold := time.Duration(global.CORE_CONFIG.Refresh.Threshold()) * 24 * time.Hour
	checked := 0
	recovered := 0
	disabled := 0

	for i := range list {
		acc := list[i]
		checked++
		now := time.Now()

		if acc.Status == model.AccountStatusCooldown && acc.CooldownUntil != nil && now.After(*acc.CooldownUntil) {
			acc.Status = model.AccountStatusEnabled
			acc.FailCount = 0
			acc.LastError = ""
			acc.CooldownUntil = nil
			_ = model.UpdateAccount(&acc)
			global.CORE_LOG.Info("watchdog cooldown expired, re-enabled", zap.Uint("account_id", acc.ID), zap.String("name", acc.Name))
		}

		if acc.Status == model.AccountStatusDisabled {
			continue
		}
		if acc.JWT == "" {
			continue
		}

		if ShouldRefresh(acc.JWT, threshold) {
			if err := w.refresher.RefreshAccount(ctx, &acc); err != nil {
				global.CORE_LOG.Warn("watchdog refresh failed", zap.Uint("account_id", acc.ID), zap.Error(err))
				w.markFail(&acc, "refresh failed: "+err.Error())
				disabled++
				continue
			}
			recovered++
		}

		if acc.Status != model.AccountStatusEnabled {
			continue
		}

		_ = model.RefreshMonthlyCycleIfNeeded(acc.ID)

		if global.CORE_CONFIG.Watchdog.SyncCredit {
			if err := w.SyncAccountCredit(ctx, &acc); err != nil {
				global.CORE_LOG.Warn("watchdog credit sync failed", zap.Uint("account_id", acc.ID), zap.Error(err))
			} else {
				latest, _ := model.GetAccountByID(acc.ID)
				if latest != nil {
					acc = *latest
					if acc.CreditSyncedAt != nil && acc.CreditRemain <= 0 {
						w.markFail(&acc, "credit exhausted")
						disabled++
						continue
					}
				}
			}
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
}

func (w *Watchdog) SyncAccountCredit(ctx context.Context, acc *model.Account) error {
	if acc == nil {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if strings.TrimSpace(acc.SessionCookie) != "" {
		snap, err := w.client.FetchUserResource(checkCtx, acc.SessionCookie)
		if err != nil {
			if notify, notifyErr := w.client.CheckDosage(checkCtx, acc); notifyErr == nil && notify != nil {
				_ = model.SaveDosageNotify(acc.ID, notify.Code, firstNonEmpty(notify.Zh, notify.En))
				if notify.Code != 0 {
					return fmt.Errorf("dosage notify code %d: %s", notify.Code, firstNonEmpty(notify.Zh, notify.En))
				}
			}
			return err
		}
		return model.SaveCreditSnapshot(acc.ID, *snap)
	}

	notify, err := w.client.CheckDosage(checkCtx, acc)
	if err != nil {
		return err
	}
	msg := firstNonEmpty(notify.Zh, notify.En)
	_ = model.SaveDosageNotify(acc.ID, notify.Code, msg)
	if notify.Code != 0 {
		return fmt.Errorf("dosage notify code %d: %s", notify.Code, msg)
	}
	return nil
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
