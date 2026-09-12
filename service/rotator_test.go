package service

import (
	"testing"
	"time"

	"codebuddy-gateway/config"
	"codebuddy-gateway/global"
	"codebuddy-gateway/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupRotatorDB(t *testing.T) func() {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Account{}); err != nil {
		t.Fatal(err)
	}
	oldDB := global.CORE_DB
	oldGW := global.CORE_CONFIG.Gateway
	oldWD := global.CORE_CONFIG.Watchdog
	global.CORE_DB = db
	global.CORE_CONFIG.Gateway.Rotate = "sticky"
	global.CORE_CONFIG.Watchdog = config.Watchdog{CooldownSeconds: 600, FailThreshold: 3}
	return func() {
		global.CORE_DB = oldDB
		global.CORE_CONFIG.Gateway = oldGW
		global.CORE_CONFIG.Watchdog = oldWD
	}
}

func addRotatorAccount(t *testing.T, name, jwt string, remain float64, synced bool, used float64) *model.Account {
	t.Helper()
	acc := &model.Account{
		Name:                name,
		JWT:                 jwt,
		Status:              model.AccountStatusEnabled,
		Weight:              1,
		MonthlyCreditRemain: remain,
		CreditUsed:          used,
	}
	if synced {
		now := time.Now()
		acc.CreditSyncedAt = &now
	}
	if err := model.CreateAccount(acc); err != nil {
		t.Fatal(err)
	}
	return acc
}

func TestStickyStaysOnSameAccount(t *testing.T) {
	defer setupRotatorDB(t)()
	a := addRotatorAccount(t, "a", "jwt-a", 10, true, 0)
	b := addRotatorAccount(t, "b", "jwt-b", 10, true, 0)
	r := NewRotator()
	first, err := r.NextFor(nil, "deepseek-v4.1-flash")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != a.ID {
		t.Fatalf("want first account %d got %d", a.ID, first.ID)
	}
	r.MarkSuccessFor(first, "deepseek-v4.1-flash")
	for i := 0; i < 5; i++ {
		got, err := r.NextFor(nil, "deepseek-v4.1-flash")
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != a.ID {
			t.Fatalf("sticky broke at %d: got %d want %d", i, got.ID, a.ID)
		}
	}
	other, err := r.NextFor(nil, "kimi-k3")
	if err != nil {
		t.Fatal(err)
	}
	if other.ID != a.ID {
		t.Fatalf("new model should still start from first unused sticky, got %d", other.ID)
	}
	_ = b
}

func TestStickySkipsZeroCreditAndMovesToNext(t *testing.T) {
	defer setupRotatorDB(t)()
	a := addRotatorAccount(t, "a", "jwt-a", 0, true, 1)
	b := addRotatorAccount(t, "b", "jwt-b", 8, true, 0)
	r := NewRotator()
	got, err := r.NextFor(nil, "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != b.ID {
		t.Fatalf("got %d want %d", got.ID, b.ID)
	}
	_ = a
}

func TestUnsyncedZeroCreditStillUsable(t *testing.T) {
	defer setupRotatorDB(t)()
	a := addRotatorAccount(t, "a", "jwt-a", 0, false, 0)
	r := NewRotator()
	got, err := r.NextFor(nil, "deepseek-v4.1-flash")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != a.ID {
		t.Fatalf("got %d want %d", got.ID, a.ID)
	}
}

func TestUnsyncedUsedCreditStillUsable(t *testing.T) {
	defer setupRotatorDB(t)()
	a := addRotatorAccount(t, "a", "jwt-a", 0, false, 37.41)
	r := NewRotator()
	got, err := r.NextFor(nil, "glm-5.3")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != a.ID {
		t.Fatalf("used-but-unsynced should still be picked, got %d want %d", got.ID, a.ID)
	}
}

func TestModelQuotaSwitchesAccount(t *testing.T) {
	defer setupRotatorDB(t)()
	a := addRotatorAccount(t, "a", "jwt-a", 20, true, 0)
	b := addRotatorAccount(t, "b", "jwt-b", 20, true, 0)
	r := NewRotator()
	first, err := r.NextFor(nil, "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != a.ID {
		t.Fatalf("got %d want %d", first.ID, a.ID)
	}
	r.MarkSuccessFor(first, "deepseek-v4-pro")
	r.MarkModelExhausted(first, "deepseek-v4-pro", "upstream 429: deepseek 额度已用尽")
	got, err := r.NextFor(nil, "deepseek-v4-pro")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != b.ID {
		t.Fatalf("after quota got %d want %d", got.ID, b.ID)
	}
	glm, err := r.NextFor(nil, "glm-5.1")
	if err != nil {
		t.Fatal(err)
	}
	if glm.ID != a.ID {
		t.Fatalf("other model should still use account a, got %d", glm.ID)
	}
}

func TestRoundRobinStillRotates(t *testing.T) {
	defer setupRotatorDB(t)()
	global.CORE_CONFIG.Gateway.Rotate = "round_robin"
	a := addRotatorAccount(t, "a", "jwt-a", 10, true, 0)
	b := addRotatorAccount(t, "b", "jwt-b", 10, true, 0)
	r := NewRotator()
	first, _ := r.NextFor(nil, "deepseek-v4.1-flash")
	second, _ := r.NextFor(nil, "deepseek-v4.1-flash")
	if first.ID != a.ID || second.ID != b.ID {
		t.Fatalf("round robin got %d then %d", first.ID, second.ID)
	}
}

func TestIsModelQuotaExhausted(t *testing.T) {
	if !isModelQuotaExhausted(429, []byte(`{"msg":"too many requests"}`)) {
		t.Fatal("429")
	}
	if !isModelQuotaExhausted(400, []byte(`{"msg":"该模型额度已用尽"}`)) {
		t.Fatal("cn quota")
	}
	if isModelQuotaExhausted(400, []byte(`{"code":11102,"msg":"model not available"}`)) {
		t.Fatal("11102 is not quota")
	}
	if isModelQuotaExhausted(400, []byte(`{"code":11128,"msg":"unapproved channel"}`)) {
		t.Fatal("11128 is not quota")
	}
}

func TestPickStickyWalksForward(t *testing.T) {
	cands := []model.Account{{CORE_MODEL: global.CORE_MODEL{ID: 1}}, {CORE_MODEL: global.CORE_MODEL{ID: 3}}, {CORE_MODEL: global.CORE_MODEL{ID: 5}}}
	got := pickSticky(cands, 3)
	if got.ID != 3 {
		t.Fatalf("sticky present got %d", got.ID)
	}
	got = pickSticky([]model.Account{cands[2]}, 3)
	if got.ID != 5 {
		t.Fatalf("next after 3 got %d", got.ID)
	}
}
