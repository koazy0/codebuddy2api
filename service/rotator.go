package service

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"codebuddy-gateway/global"
	"codebuddy-gateway/model"
)

type Rotator struct {
	mu      sync.Mutex
	index   int
	sticky  map[string]uint
	blocked map[string]time.Time
}

func NewRotator() *Rotator {
	return &Rotator{
		sticky:  map[string]uint{},
		blocked: map[string]time.Time{},
	}
}

func (r *Rotator) Next(exclude map[uint]struct{}) (*model.Account, error) {
	return r.NextFor(exclude, "")
}

func (r *Rotator) NextFor(exclude map[uint]struct{}, modelName string) (*model.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	list, err := model.ListEnabledAccounts()
	if err != nil {
		return nil, err
	}
	modelName = normalizeStickyModel(modelName)
	now := time.Now()
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
		if !accountHasCredit(acc) {
			continue
		}
		if r.isBlockedLocked(acc.ID, modelName, now) {
			continue
		}
		candidates = append(candidates, acc)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no available codebuddy account")
	}

	mode := rotateMode()
	var picked model.Account
	switch mode {
	case "least_used":
		picked = pickLeastUsed(candidates)
	case "round_robin":
		start := r.index % len(candidates)
		r.index = (start + 1) % len(candidates)
		picked = candidates[start]
	default:
		picked = pickSticky(candidates, r.sticky[modelName])
	}
	return cloneAccount(picked), nil
}

func (r *Rotator) MarkSuccess(acc *model.Account) {
	r.MarkSuccessFor(acc, "")
}

func (r *Rotator) MarkSuccessFor(acc *model.Account, modelName string) {
	if acc == nil {
		return
	}
	if m := normalizeStickyModel(modelName); m != "" {
		r.mu.Lock()
		if r.sticky == nil {
			r.sticky = map[string]uint{}
		}
		r.sticky[m] = acc.ID
		r.mu.Unlock()
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

func (r *Rotator) MarkModelExhausted(acc *model.Account, modelName, errMsg string) {
	if acc == nil {
		return
	}
	modelName = normalizeStickyModel(modelName)
	until := time.Now().Add(quotaBlockDuration(errMsg))
	r.mu.Lock()
	if r.blocked == nil {
		r.blocked = map[string]time.Time{}
	}
	r.blocked[blockKey(acc.ID, modelName)] = until
	if r.sticky != nil && r.sticky[modelName] == acc.ID {
		delete(r.sticky, modelName)
	}
	r.mu.Unlock()
	_ = model.MarkAccountFailure(acc.ID, errMsg, 0, model.AccountStatusEnabled, nil)
}

func (r *Rotator) StickyAccount(modelName string) uint {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sticky[normalizeStickyModel(modelName)]
}

func (r *Rotator) isBlockedLocked(accountID uint, modelName string, now time.Time) bool {
	if r.blocked == nil || modelName == "" {
		return false
	}
	until, ok := r.blocked[blockKey(accountID, modelName)]
	if !ok {
		return false
	}
	if now.Before(until) {
		return true
	}
	delete(r.blocked, blockKey(accountID, modelName))
	return false
}

func rotateMode() string {
	m := strings.ToLower(strings.TrimSpace(global.CORE_CONFIG.Gateway.Rotate))
	if m == "" {
		return "sticky"
	}
	return m
}

func normalizeStickyModel(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func blockKey(accountID uint, modelName string) string {
	return fmt.Sprintf("%d|%s", accountID, modelName)
}

func accountHasCredit(acc model.Account) bool {
	// 没同步过账单时 remain 默认就是 0，不能当成没额度。
	// 只有真正拉到过 user-resource 且剩余 <= 0，才跳过这个号。
	if acc.CreditSyncedAt == nil {
		return true
	}
	return acc.MonthlyCreditRemain+acc.OnetimeCreditRemain > 0
}

func pickLeastUsed(candidates []model.Account) model.Account {
	picked := candidates[0]
	for i := 1; i < len(candidates); i++ {
		if candidates[i].LastUsedAt == nil {
			return candidates[i]
		}
		if picked.LastUsedAt != nil && candidates[i].LastUsedAt.Before(*picked.LastUsedAt) {
			picked = candidates[i]
		}
	}
	return picked
}

func pickSticky(candidates []model.Account, stickyID uint) model.Account {
	if stickyID != 0 {
		for _, acc := range candidates {
			if acc.ID == stickyID {
				return acc
			}
		}
		for _, acc := range candidates {
			if acc.ID > stickyID {
				return acc
			}
		}
	}
	return candidates[0]
}

func cloneAccount(acc model.Account) *model.Account {
	cp := acc
	return &cp
}

func quotaBlockDuration(errMsg string) time.Duration {
	s := strings.ToLower(errMsg)
	if strings.Contains(s, "today") || strings.Contains(s, "每日") || strings.Contains(s, "当天") || strings.Contains(s, "本日") {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 1, 0, 0, now.Location())
		return next.Sub(now)
	}
	sec := global.CORE_CONFIG.Watchdog.Cooldown()
	if sec <= 0 {
		sec = 600
	}
	return time.Duration(sec) * time.Second
}
