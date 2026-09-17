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

	// 成本分层硬过滤：同样能跑，优先挑不花钱的号。
	//
	// 上游对不同账号在同一模型上的计费不一致（实测同名模型跨账号单价可差
	// 数十倍），而是否收费只能靠响应里的 credit 观察，无法从配置推断。
	// 这里按实测结果分层，只保留最优层：
	//   有免费号 → 只用免费号；
	//   没有免费的 → 退到无观测的号（其中可能藏着尚未发现是免费的）；
	//   只有收费观测 → 用最便宜那层。
	//
	// 用硬过滤而非仅排序，是因为后面各模式都会在候选内挑（sticky 粘号、
	// round_robin 轮转）。只排序的话收费号仍会被选中，达不到「优先免费」。
	candidates = filterByCostTier(candidates, modelName)

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

// filterByCostTier 只保留成本最优的那一层候选。
//
// 模型名为空（未指定模型，如无模型上下文的调用）时不分层——成本是按
// (账号, 模型) 观测的，没有模型就无从判定，硬分层会误伤。
//
// 全部候选同层时原样返回（最常见情况：都没观测），不重建切片。
func filterByCostTier(candidates []model.Account, modelName string) []model.Account {
	if modelName == "" || len(candidates) <= 1 {
		return candidates
	}
	best := costTierPaid
	same := true
	tiers := make([]costTier, len(candidates))
	for i := range candidates {
		ti, _ := defaultCostLedger.tierOf(candidates[i].ID, modelName)
		tiers[i] = ti
		if ti < best {
			best = ti
		}
		if i > 0 && ti != tiers[0] {
			same = false
		}
	}
	if same {
		return candidates
	}
	kept := make([]model.Account, 0, len(candidates))
	for i := range candidates {
		if tiers[i] == best {
			kept = append(kept, candidates[i])
		}
	}
	if len(kept) == 0 {
		return candidates
	}
	return kept
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

// accountHasCredit 判断账号是否有可用额度。
//
// 「没同步过」不能当成「没额度」：CreditRemain 的零值是 0，把未同步的账号
// 判成无额度会让新导入的账号一上来就被跳过。只有真正拿到过账单快照、
// 且剩余确实 <= 0，才算耗尽。
//
// 这是全项目唯一的额度判定入口：轮询、看门狗、批量任务都读它，
// 避免各写一份导致「一处认为有额度、另一处认为没有」的分叉。
func accountHasCredit(acc model.Account) bool {
	if acc.CreditSyncedAt == nil {
		return true
	}
	return creditRemainOf(acc) > 0
}

// creditRemainOf 汇总账号的可用额度（月度 + 一次性）。
func creditRemainOf(acc model.Account) float64 {
	return acc.MonthlyCreditRemain + acc.OnetimeCreditRemain
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
