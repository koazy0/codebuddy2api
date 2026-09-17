package service

import (
	"testing"
	"time"

	"codebuddy-gateway/model"
)

// newTestLedger 造一个隔离的账本，避免测试之间互相污染。
func newTestLedger() *costLedger {
	return &costLedger{
		entries: map[string]modelCostEntry{},
		gcEvery: time.Hour, // 测试里不触发 GC，需要时可手工调
		nowFn:   time.Now,
	}
}

// TestCostTierBasicTiers 三种分层的基本判定：
// 免费=0、无观测=1、收费=2。
func TestCostTierBasicTiers(t *testing.T) {
	l := newTestLedger()

	// 无观测 → tier1
	if ti, _ := l.tierOf(1, "glm-5.2"); ti != costTierUnknown {
		t.Fatalf("无观测应为 unknown，实际 %v", ti)
	}
	// 实测免费（credit=0）→ tier0
	l.note(1, "glm-5.2", 0, 1000)
	if ti, _ := l.tierOf(1, "glm-5.2"); ti != costTierFree {
		t.Fatalf("credit=0 应为 free，实际 %v", ti)
	}
	// 实测收费 → tier2
	l.note(2, "glm-5.2", 0.18, 1000)
	ti, price := l.tierOf(2, "glm-5.2")
	if ti != costTierPaid {
		t.Fatalf("credit>0 应为 paid，实际 %v", ti)
	}
	if price <= 0 {
		t.Fatalf("收费层应带单价，实际 %v", price)
	}
}

// TestCostTierZeroTokensIgnored 是防「未知被污染成免费」的关键：
// tokens 为 0 时无法算单价，必须跳过而不是记成 0 单价。
//
// 如果记成 0 单价，这个号会被判成 tier0 并垄断该模型全部流量——
// 而它实际上可能收费。
func TestCostTierZeroTokensIgnored(t *testing.T) {
	l := newTestLedger()
	l.note(1, "glm-5.2", 0, 0) // 缺 usage：不应写入
	if ti, _ := l.tierOf(1, "glm-5.2"); ti != costTierUnknown {
		t.Fatalf("tokens=0 的观测应被忽略，实际判成 %v", ti)
	}
}

// TestCostTierExpires 时段性优惠必须过期。
// 夜间免费的号白天不该还算免费。
func TestCostTierExpires(t *testing.T) {
	l := newTestLedger()
	base := time.Now()
	l.nowFn = func() time.Time { return base }
	l.note(1, "glm-5.2", 0, 1000)
	if ti, _ := l.tierOf(1, "glm-5.2"); ti != costTierFree {
		t.Fatal("前置条件不成立：应判为免费")
	}
	// 时间推过 TTL
	l.nowFn = func() time.Time { return base.Add(modelCostTTL + time.Minute) }
	if ti, _ := l.tierOf(1, "glm-5.2"); ti != costTierUnknown {
		t.Fatalf("过期观测应回落为 unknown，实际 %v", ti)
	}
}

// TestCostTierFreeToPaidFlipsImmediately 优惠切换要立刻生效。
// 取平均会把「夜间免费 → 白天收费」拖很久才反映出来。
func TestCostTierFreeToPaidFlipsImmediately(t *testing.T) {
	l := newTestLedger()
	// 先多次免费观测
	for i := 0; i < 5; i++ {
		l.note(1, "glm-5.2", 0, 1000)
	}
	if ti, _ := l.tierOf(1, "glm-5.2"); ti != costTierFree {
		t.Fatal("前置条件：应判为免费")
	}
	// 一次收费观测 → 立刻转为收费
	l.note(1, "glm-5.2", 0.2, 1000)
	if ti, _ := l.tierOf(1, "glm-5.2"); ti != costTierPaid {
		t.Fatalf("免费→收费应立刻翻转，实际 %v", ti)
	}
}

// TestCostTierPaidToFreeFlipsImmediately 反向同理。
func TestCostTierPaidToFreeFlipsImmediately(t *testing.T) {
	l := newTestLedger()
	for i := 0; i < 5; i++ {
		l.note(1, "glm-5.2", 0.2, 1000)
	}
	l.note(1, "glm-5.2", 0, 1000)
	if ti, _ := l.tierOf(1, "glm-5.2"); ti != costTierFree {
		t.Fatalf("收费→免费应立刻翻转，实际 %v", ti)
	}
}

// TestFilterByCostTierPrefersFree 硬过滤的核心语义：有免费号就只用免费号。
func TestFilterByCostTierPrefersFree(t *testing.T) {
	old := defaultCostLedger
	defer func() { defaultCostLedger = old }()
	l := newTestLedger()
	defaultCostLedger = l

	l.note(1, "m", 0, 1000)   // 免费
	l.note(2, "m", 0.5, 1000) // 收费
	// 3 无观测

	cands := []model.Account{{Name: "paid"}, {Name: "free"}, {Name: "unknown"}}
	cands[0].ID, cands[1].ID, cands[2].ID = 2, 1, 3

	kept := filterByCostTier(cands, "m")
	if len(kept) != 1 || kept[0].ID != 1 {
		t.Fatalf("应只保留免费号，实际 %+v", kept)
	}
}

// TestFilterByCostTierFallsBackToUnknown 没有免费号时退到无观测层，
// 而不是直接跳到收费层——未知号里可能藏着尚未发现是免费的。
func TestFilterByCostTierFallsBackToUnknown(t *testing.T) {
	old := defaultCostLedger
	defer func() { defaultCostLedger = old }()
	l := newTestLedger()
	defaultCostLedger = l

	l.note(1, "m", 0.5, 1000) // 已观测收费
	// 2、3 无观测

	cands := []model.Account{{Name: "paid"}, {Name: "u1"}, {Name: "u2"}}
	cands[0].ID, cands[1].ID, cands[2].ID = 1, 2, 3

	kept := filterByCostTier(cands, "m")
	if len(kept) != 2 {
		t.Fatalf("应保留两个无观测号，实际 %d 个: %+v", len(kept), kept)
	}
	for _, a := range kept {
		if a.ID == 1 {
			t.Fatal("已观测收费的号不该留下")
		}
	}
}

// TestFilterByCostTierSameTierNoRebuild 全员同层时原样返回。
// 这是最常见情况（都没观测），不该产生额外分配。
func TestFilterByCostTierSameTierNoRebuild(t *testing.T) {
	old := defaultCostLedger
	defer func() { defaultCostLedger = old }()
	defaultCostLedger = newTestLedger()

	cands := []model.Account{{Name: "a"}, {Name: "b"}}
	cands[0].ID, cands[1].ID = 1, 2

	kept := filterByCostTier(cands, "m")
	if len(kept) != 2 {
		t.Fatalf("同层不应过滤，实际 %d", len(kept))
	}
	if &kept[0] != &cands[0] {
		t.Fatal("同层时应原样返回，不该重建切片")
	}
}

// TestFilterByCostTierEmptyModelSkips 模型名为空时不分层——
// 成本按 (账号, 模型) 观测，没有模型就无从判定。
func TestFilterByCostTierEmptyModelSkips(t *testing.T) {
	old := defaultCostLedger
	defer func() { defaultCostLedger = old }()
	l := newTestLedger()
	defaultCostLedger = l
	l.note(1, "", 0, 1000)

	cands := []model.Account{{Name: "a"}, {Name: "b"}}
	cands[0].ID, cands[1].ID = 1, 2

	kept := filterByCostTier(cands, "")
	if len(kept) != 2 {
		t.Fatalf("模型名为空不应过滤，实际 %d", len(kept))
	}
}

// TestLedgerGCReclaimsExpired 过期条目要被回收，否则 map 只增不减。
func TestLedgerGCReclaimsExpired(t *testing.T) {
	l := newTestLedger()
	l.gcEvery = 0 // 每次 note 都可触发
	base := time.Now()
	l.nowFn = func() time.Time { return base }

	l.note(1, "m", 0.5, 1000)
	if len(l.entries) != 1 {
		t.Fatalf("前置条件：应有 1 条，实际 %d", len(l.entries))
	}
	// 推过 TTL 后再写一条，触发 GC
	l.nowFn = func() time.Time { return base.Add(modelCostTTL + time.Hour) }
	l.note(2, "m", 0.5, 1000)

	if _, ok := l.entries[costKey(1, "m")]; ok {
		t.Fatal("过期条目未被回收")
	}
	if _, ok := l.entries[costKey(2, "m")]; !ok {
		t.Fatal("新条目不该被误删")
	}
}

// TestLedgerStatsCountsOnlyFresh 统计只算未过期的条目。
func TestLedgerStatsCountsOnlyFresh(t *testing.T) {
	l := newTestLedger()
	base := time.Now()
	l.nowFn = func() time.Time { return base }
	l.note(1, "m", 0, 1000)   // free
	l.note(2, "m", 0.5, 1000) // paid
	l.note(3, "m", 0, 1000)   // free

	total, free, paid := l.stats()
	if total != 3 || free != 2 || paid != 1 {
		t.Fatalf("统计错误: total=%d free=%d paid=%d", total, free, paid)
	}
	// 全部过期后应为 0
	l.nowFn = func() time.Time { return base.Add(modelCostTTL + time.Hour) }
	if total, free, paid := l.stats(); total != 0 || free != 0 || paid != 0 {
		t.Fatalf("过期后统计应清零: total=%d free=%d paid=%d", total, free, paid)
	}
}

// TestLedgerIsolatesModels 不同模型的观测互不干扰。
func TestLedgerIsolatesModels(t *testing.T) {
	l := newTestLedger()
	l.note(1, "glm-5.2", 0, 1000)    // 在 glm-5.2 上免费
	l.note(1, "deepseek", 0.5, 1000) // 在 deepseek 上收费

	if ti, _ := l.tierOf(1, "glm-5.2"); ti != costTierFree {
		t.Fatalf("glm-5.2 应免费，实际 %v", ti)
	}
	if ti, _ := l.tierOf(1, "deepseek"); ti != costTierPaid {
		t.Fatalf("deepseek 应收费，实际 %v", ti)
	}
}

// TestLedgerModelNameNormalized 模型名大小写/空格应归一，
// 否则同一个模型会被记成两笔互不可见的账。
func TestLedgerModelNameNormalized(t *testing.T) {
	l := newTestLedger()
	l.note(1, "GLM-5.2", 0, 1000)
	if ti, _ := l.tierOf(1, " glm-5.2 "); ti != costTierFree {
		t.Fatalf("模型名应归一化，实际 %v", ti)
	}
}
