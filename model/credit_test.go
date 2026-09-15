package model

import "testing"

func TestDeductCreditMonthlyFirst(t *testing.T) {
	got := deductCredit(100, 50, 30)
	if got.Source != "monthly" || got.Monthly != 30 || got.Onetime != 0 {
		t.Fatalf("%+v", got)
	}
	if got.MonthlyRemain != 70 || got.OnetimeRemain != 50 || got.Remain != 120 {
		t.Fatalf("remain %+v", got)
	}
}

func TestDeductCreditSpillToOnetime(t *testing.T) {
	got := deductCredit(10, 50, 30)
	if got.Source != "mixed" || got.Monthly != 10 || got.Onetime != 20 {
		t.Fatalf("%+v", got)
	}
	if got.Remain != 30 {
		t.Fatalf("remain=%v", got.Remain)
	}
}

func TestDeductCreditOnetimeOnly(t *testing.T) {
	got := deductCredit(0, 80, 25)
	if got.Source != "onetime" || got.Onetime != 25 || got.MonthlyRemain != 0 || got.OnetimeRemain != 55 {
		t.Fatalf("%+v", got)
	}
}

func TestDeductCreditUntracked(t *testing.T) {
	got := deductCredit(0, 0, 5)
	if got.Source != "untracked" || got.Remain != 0 {
		t.Fatalf("%+v", got)
	}
}

// TestUIDHasNoSideEffect UID() 必须是纯读：解析结果只返回，不写回结构体。
// 带写副作用会让它在并发任务链路上成为数据竞争点（多个 goroutine
// 同时写 a.UserID），而 JWT 解析本身极快，没有缓存的必要。
func TestUIDHasNoSideEffect(t *testing.T) {
	token := "eyJhbGciOiJSUzI1NiJ9." +
		"eyJzdWIiOiIyOTkzYWZiYi0wMGIyLTQ1NTItOTljNS0xZjEzMTFmZjgzZmMifQ.sig"
	acc := &Account{JWT: token}
	got := acc.UID()
	if got != "2993afbb-00b2-4552-99c5-1f1311ff83fc" {
		t.Fatalf("应从 JWT sub 解析，实际 %q", got)
	}
	if acc.UserID != "" {
		t.Fatalf("UID() 不应写回字段，实际写成 %q", acc.UserID)
	}
	// 幂等：多次调用结果一致
	if again := acc.UID(); again != got {
		t.Fatalf("多次调用应一致: %q vs %q", again, got)
	}
	// 已落库值优先
	acc2 := &Account{UserID: "explicit"}
	if got := acc2.UID(); got != "explicit" {
		t.Fatalf("已落库值应优先，实际 %q", got)
	}
}
