package service

import (
	"fmt"
	"sync"
	"time"

	"codebuddy-gateway/global"
	"codebuddy-gateway/model"
)

type Rotator struct {
	mu    sync.Mutex
	index int
}

func NewRotator() *Rotator {
	return &Rotator{}
}

func (r *Rotator) Next(exclude map[uint]struct{}) (*model.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	list, err := model.ListEnabledAccounts()
	if err != nil {
		return nil, err
	}
	candidates := make([]model.Account, 0, len(list))
	for _, acc := range list {
		if exclude != nil {
			if _, ok := exclude[acc.ID]; ok {
				continue
			}
		}
		if acc.JWT == "" {
			continue
		}
		if acc.CreditSyncedAt != nil && acc.MonthlyCreditRemain+acc.OnetimeCreditRemain <= 0 {
			continue
		}
		candidates = append(candidates, acc)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no available codebuddy account")
	}

	mode := global.CORE_CONFIG.Gateway.Rotate
	if mode == "least_used" {
		picked := candidates[0]
		for i := 1; i < len(candidates); i++ {
			if candidates[i].LastUsedAt == nil {
				picked = candidates[i]
				break
			}
			if picked.LastUsedAt != nil && candidates[i].LastUsedAt.Before(*picked.LastUsedAt) {
				picked = candidates[i]
			}
		}
		cp := picked
		return &cp, nil
	}

	start := r.index % len(candidates)
	r.index = (start + 1) % len(candidates)
	cp := candidates[start]
	return &cp, nil
}

func (r *Rotator) MarkSuccess(acc *model.Account) {
	if acc == nil {
		return
	}
	_ = model.MarkAccountUsed(acc.ID)
}

func (r *Rotator) MarkFailure(acc *model.Account, errMsg string) {
	if acc == nil {
		return
	}
	latest, err := model.GetAccountByID(acc.ID)
	if err != nil {
		return
	}
	fail := latest.FailCount + 1
	status := model.AccountStatusEnabled
	var cooldown *time.Time
	limit := global.CORE_CONFIG.Watchdog.FailLimit()
	if fail >= limit {
		status = model.AccountStatusCooldown
		until := time.Now().Add(time.Duration(global.CORE_CONFIG.Watchdog.Cooldown()) * time.Second)
		cooldown = &until
	}
	_ = model.MarkAccountFailure(acc.ID, errMsg, fail, status, cooldown)
}
