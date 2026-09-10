package service

import "testing"

func TestParseUserResource(t *testing.T) {
	raw := []byte(`{
	  "code": 0,
	  "data": {
	    "Response": {
	      "Data": {
	        "TotalDosage": 4600,
	        "Accounts": [
	          {
	            "PackageName": "CodeBuddy个人体验版",
	            "CapacityType": 4,
	            "CapacitySize": 500,
	            "CapacityRemain": 500,
	            "CycleCapacityRemainPrecise": "499.11",
	            "CycleCapacitySize": 500,
	            "CycleStartTime": "2026-04-01 00:00:00",
	            "CycleEndTime": "2026-04-30 23:59:59"
	          },
	          {
	            "PackageName": "裂变包",
	            "CapacityType": 1,
	            "CapacitySize": 3000,
	            "CapacityRemainPrecise": "3000"
	          },
	          {
	            "PackageName": "裂变包",
	            "CapacityType": 1,
	            "CapacitySize": 100,
	            "CapacityRemainPrecise": "80.5"
	          }
	        ]
	      }
	    }
	  }
	}`)
	snap, err := ParseUserResource(raw)
	if err != nil {
		t.Fatal(err)
	}
	if snap.MonthlyTotal != 500 || snap.MonthlyRemain != 499.11 {
		t.Fatalf("monthly %+v", snap)
	}
	if snap.OnetimeTotal != 3100 || snap.OnetimeRemain != 3080.5 {
		t.Fatalf("onetime %+v", snap)
	}
	if snap.CycleStart == nil || snap.CycleEnd == nil {
		t.Fatal("cycle missing")
	}
}

func TestExtractUsageThinkingAndCache(t *testing.T) {
	chunk := map[string]any{
		"id": "abc",
		"usage": map[string]any{
			"prompt_tokens":            174930.0,
			"completion_tokens":        31.0,
			"total_tokens":             174961.0,
			"credit":                   7.53,
			"prompt_cache_hit_tokens":  174400.0,
			"prompt_cache_miss_tokens": 530.0,
			"completion_tokens_details": map[string]any{
				"reasoning_tokens": 14.0,
			},
		},
	}
	u := extractUsage(chunk)
	if u == nil {
		t.Fatal("nil")
	}
	if u.ThinkingTokens != 14 || u.Credit != 7.53 || u.CacheHitTokens != 174400 {
		t.Fatalf("%+v", u)
	}
	deriveMetrics(u, 2000)
	if u.CacheHitRate < 0.99 {
		t.Fatalf("hit rate %v", u.CacheHitRate)
	}
	if u.TokensPerSecond <= 0 {
		t.Fatalf("tps %v", u.TokensPerSecond)
	}
}

func TestEstimateCredit(t *testing.T) {
	if estimateCredit(0, 0) != 0 {
		t.Fatal("zero")
	}
	if estimateCredit(1_000_000, 0) != 45 {
		t.Fatalf("input %v", estimateCredit(1_000_000, 0))
	}
}
