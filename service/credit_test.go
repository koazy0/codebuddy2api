package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"codebuddy-gateway/global"
	"codebuddy-gateway/model"
)

// TestParseUserResource 锁定真实的字段语义。
//
// 关键在于「可用额度取哪个字段」，这两个很容易搞反（实测确认）：
//
//	CapacityRemain*      整包从发放至今的剩余 —— 对体验版恒为 500，不随消耗递减
//	CycleCapacityRemain* 本计费周期剩余       —— 真正能用的额度
//
// 证据：CycleCapacityUsed=500 且 CycleCapacityRemain=0 的账号，实际调用一律
// 返回 429「额度已用尽」，而其 CapacityRemain 仍是 500。若按 CapacityRemain
// 判断，就会把「已耗尽」读成「还剩 500」，表现为「显示有额度、一发就报耗尽」。
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
	            "CapacityRemainPrecise": "500",
	            "CycleCapacityRemain": 0,
	            "CycleCapacityRemainPrecise": "139.11",
	            "CycleCapacitySize": 500,
	            "CycleStartTime": "2026-04-01 00:00:00",
	            "CycleEndTime": "2026-04-30 23:59:59"
	          },
	          {
	            "PackageName": "裂变包",
	            "CapacityType": 1,
	            "CapacitySize": 3000,
	            "CapacityRemainPrecise": "3000",
	            "CycleCapacityRemainPrecise": "3000"
	          },
	          {
	            "PackageName": "裂变包",
	            "CapacityType": 1,
	            "CapacitySize": 100,
	            "CapacityRemainPrecise": "80.5",
	            "CycleCapacityRemainPrecise": "80.5"
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
	// 月度包：可用额度取周期剩余 139.11（不是整包的 500）
	if snap.MonthlyRemain != 139.11 {
		t.Fatalf("月度可用额度应取周期剩余 139.11，实际 %v", snap.MonthlyRemain)
	}
	if snap.MonthlyTotal != 500 {
		t.Fatalf("月度总额应为 500，实际 %v", snap.MonthlyTotal)
	}
	if snap.OnetimeRemain != 3080.5 {
		t.Fatalf("一次性额度解析错误: %+v", snap)
	}
	if snap.CycleStart == nil || snap.CycleEnd == nil {
		t.Fatal("计费周期缺失")
	}
}

// TestParseUserResourceExhaustedStaysZero 钉住最要紧的一条：
// 周期额度用尽（CycleCapacityRemain=0）时，可用额度必须是 0，
// 哪怕整包 CapacityRemain 还写着 500。
//
// 这正是线上「显示有额度、一发就报额度已用尽」的成因。
func TestParseUserResourceExhaustedStaysZero(t *testing.T) {
	raw := []byte(`{"code":0,"data":{"Response":{"Data":{"Accounts":[
	  {"CapacityType":4,"CapacitySizePrecise":"500",
	   "CapacityRemainPrecise":"500",
	   "CycleCapacitySizePrecise":"500",
	   "CycleCapacityRemainPrecise":"0",
	   "CycleCapacityUsedPrecise":"500"}]}}}}`)
	snap, err := ParseUserResource(raw)
	if err != nil {
		t.Fatal(err)
	}
	if snap.MonthlyRemain != 0 {
		t.Fatalf("周期已耗尽时可用额度必须为 0，实际 %v（CapacityRemain 的 500 是假值）", snap.MonthlyRemain)
	}
}

// TestParseUserResourceFallsBackToCapacity 兜底：上游只给整包字段时
// 仍要能解析出非零值（字段改名时不至于全盘归零）。
func TestParseUserResourceFallsBackToCapacity(t *testing.T) {
	raw := []byte(`{"code":0,"data":{"Response":{"Data":{"Accounts":[
	  {"CapacityType":4,"CapacitySize":500,"CapacityRemainPrecise":"321.5"}]}}}}`)
	snap, err := ParseUserResource(raw)
	if err != nil {
		t.Fatal(err)
	}
	if snap.MonthlyRemain != 321.5 {
		t.Fatalf("缺周期字段时应回落到整包剩余，实际 %v", snap.MonthlyRemain)
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

func TestFetchUserResourceByJWT(t *testing.T) {
	raw := `{
	  "code": 0,
	  "data": {
	    "Response": {
	      "Data": {
	        "Accounts": [
	          {
	            "PackageName": "CodeBuddy个人体验版",
	            "CapacityType": 4,
	            "CycleCapacityRemainPrecise": "12.5",
	            "CycleCapacitySize": 500
	          }
	        ]
	      }
	    }
	  }
	}`
	old := global.CORE_CONFIG.Gateway.Upstream
	global.CORE_CONFIG.Gateway.Upstream = "http://codebuddy.test"
	t.Cleanup(func() { global.CORE_CONFIG.Gateway.Upstream = old })
	client := &UpstreamClient{httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v2/billing/meter/get-user-resource" {
			t.Errorf("path=%s", req.URL.Path)
		}
		if !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer jwt-token") {
			t.Errorf("authorization=%s", req.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(raw)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}}
	snap, err := client.FetchUserResourceByJWT(context.Background(), &model.Account{JWT: "jwt-token"})
	if err != nil {
		t.Fatal(err)
	}
	if snap.MonthlyRemain != 12.5 || snap.MonthlyTotal != 500 {
		t.Fatalf("%+v", snap)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
