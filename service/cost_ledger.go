package service

import (
	"strconv"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// 实测成本账本
//
// 目的：让选号优先挑「跑这个模型不花钱」的账号。
//
// 上游对不同账号在同一模型上的计费并不一致——实测数据里
// deepseek-v4.1-flash 有 237 个账号在用，单价从 0 到 0.18 分/1k 不等；
// kimi-k3-1 的跨账号差异达到 38 倍。同一个请求走不同的号，成本可能差一倍以上。
//
// 关键在于「实测」：账号是否收费无法从配置推断（限免、夜间免费、试用期
// 都只体现在响应里），所以只能从每次请求返回的 usage.credit 观察。
//
// 这就是成本账本要记录的东西：(账号, 模型) → 每千 token 单价。
// ---------------------------------------------------------------------------

// modelCostTTL 是成本观测的有效期。
//
// 必须带过期，因为优惠是时段性的：夜间免费的账号到了白天照样收费，
// 拿夜间的观测去指导白天的选号会把成本算反。参考实现同样取 6 小时。
const modelCostTTL = 6 * time.Hour

// modelCostEntry 单个 (账号, 模型) 的成本观测。
type modelCostEntry struct {
	// CostPer1k 每千 token 的实测单价（credit）。
	CostPer1k float64
	// LastSeen 最后一次观测时间，用于 TTL 判定。
	LastSeen time.Time
	// Samples 累计观测次数，便于排障时判断可信度。
	Samples int
}

// costLedger 是并发安全的成本账本。
//
// key 用 "账号ID|模型" 字符串而不是嵌套 map：查询是热点路径（每次选号都查），
// 扁平结构的哈希开销更低，也省掉两层锁。
type costLedger struct {
	mu      sync.RWMutex
	entries map[string]modelCostEntry
	lastGC  time.Time
	gcEvery time.Duration
	nowFn   func() time.Time
}

var defaultCostLedger = &costLedger{
	entries: map[string]modelCostEntry{},
	gcEvery: 10 * time.Minute,
	nowFn:   time.Now,
}

// costTier 是成本分层结果。
//
//	0 = 已实测免费（最强偏好）
//	1 = 无观测（含观测过期）
//	2 = 已实测收费
//
// 「无观测」排在「已实测收费」之前是有意的：新号的限免状态只能靠实测发现，
// 若已知收费的号恒压过未知号，那些可能免费的号永远轮不到，也就永远学不到。
type costTier int

const (
	costTierFree    costTier = 0
	costTierUnknown costTier = 1
	costTierPaid    costTier = 2
)

func costKey(accountID uint, modelName string) string {
	return strconv.FormatUint(uint64(accountID), 10) + "|" + normalizeStickyModel(modelName)
}

// note 记录一次实测扣费观测。
//
// credit 为本次请求实际扣费，tokens 为总 token 数。tokens 为 0 时无法算单价
// （除零），此时跳过——「没观测到 usage」不等于「测得 0 token」，写进去会
// 把未知污染成免费，那会让这个号被误判成 tier0 并垄断流量。
func (l *costLedger) note(accountID uint, modelName string, credit float64, tokens int) {
	if l == nil || accountID == 0 || tokens <= 0 {
		return
	}
	modelName = normalizeStickyModel(modelName)
	if modelName == "" {
		return
	}
	per1k := credit * 1000 / float64(tokens)

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = map[string]modelCostEntry{}
	}
	key := costKey(accountID, modelName)
	prev, ok := l.entries[key]
	entry := modelCostEntry{CostPer1k: per1k, LastSeen: l.now(), Samples: 1}
	if ok {
		entry.Samples = prev.Samples + 1
		// 单价取滑动平均而不是直接覆盖：单次请求的 token 量差异很大，
		// 用单次样本会被短请求放大。按样本数加权能让观测更快收敛。
		// 收费状态（0 与非 0）的翻转直接采用新值——优惠切换必须立刻生效，
		// 平均会把夜间免费拖很久才反映到白天。
		if (prev.CostPer1k == 0) != (per1k == 0) {
			entry.CostPer1k = per1k
		} else {
			n := float64(entry.Samples)
			entry.CostPer1k = (prev.CostPer1k*(n-1) + per1k) / n
		}
	}
	l.entries[key] = entry
	l.gcLocked(l.now())
}

// tierOf 返回该 (账号, 模型) 的成本分层与单价。
func (l *costLedger) tierOf(accountID uint, modelName string) (costTier, float64) {
	if l == nil {
		return costTierUnknown, 0
	}
	modelName = normalizeStickyModel(modelName)
	if modelName == "" {
		return costTierUnknown, 0
	}
	l.mu.RLock()
	entry, ok := l.entries[costKey(accountID, modelName)]
	l.mu.RUnlock()
	if !ok || entry.LastSeen.IsZero() {
		return costTierUnknown, 0
	}
	if l.now().Sub(entry.LastSeen) > modelCostTTL {
		return costTierUnknown, 0
	}
	if entry.CostPer1k <= 0 {
		return costTierFree, 0
	}
	return costTierPaid, entry.CostPer1k
}

// gcLocked 惰性回收过期条目，避免 map 只增不减。
// 调用方须已持写锁。
//
// gcEvery <= 0 表示每次都回收（测试与排障用）；默认 10 分钟一次，
// 避免每个请求都遍历整个账本。
func (l *costLedger) gcLocked(now time.Time) {
	if l.gcEvery > 0 && now.Sub(l.lastGC) < l.gcEvery {
		return
	}
	l.lastGC = now
	for k, e := range l.entries {
		if now.Sub(e.LastSeen) > modelCostTTL {
			delete(l.entries, k)
		}
	}
}

// stats 返回账本规模，供排障查看。
func (l *costLedger) stats() (total, free, paid int) {
	if l == nil {
		return 0, 0, 0
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	now := l.now()
	for _, e := range l.entries {
		if now.Sub(e.LastSeen) > modelCostTTL {
			continue
		}
		total++
		if e.CostPer1k <= 0 {
			free++
		} else {
			paid++
		}
	}
	return
}

func (l *costLedger) now() time.Time {
	if l.nowFn != nil {
		return l.nowFn()
	}
	return time.Now()
}

// NoteModelCost 记录一次实测扣费（供 proxy 在响应处理后调用）。
func NoteModelCost(accountID uint, modelName string, credit float64, tokens int) {
	defaultCostLedger.note(accountID, modelName, credit, tokens)
}

// CostLedgerStats 返回账本统计，供管理接口展示。
func CostLedgerStats() (total, free, paid int) {
	return defaultCostLedger.stats()
}
