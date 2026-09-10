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
