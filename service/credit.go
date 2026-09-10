package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"codebuddy-gateway/global"
	"codebuddy-gateway/model"
)

func estimateCredit(promptTokens, completionTokens int) float64 {
	return float64(promptTokens)*45.0/1_000_000.0 + float64(completionTokens)*634.0/1_000_000.0
}

type DosageNotify struct {
	Code int
	Zh   string
	En   string
}

func (c *UpstreamClient) CheckDosage(ctx context.Context, acc *model.Account) (*DosageNotify, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(global.CORE_CONFIG.Gateway.UpstreamBase(), "/v2/billing/meter/get-dosage-notify"), strings.NewReader(`{"timeout":5000}`))
	if err != nil {
		return nil, err
	}
	applyCodeBuddyHeaders(req, acc.JWT, global.CORE_CONFIG.CodeBuddy.HeaderAgentIntent())
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dosage http %d: %s", resp.StatusCode, clip(raw, 300))
	}
	var parsed struct {
		Code int `json:"code"`
		Data struct {
			DosageNotifyCode int    `json:"dosageNotifyCode"`
			DosageNotifyZh   string `json:"dosageNotifyZh"`
			DosageNotifyEn   string `json:"dosageNotifyEn"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	if parsed.Code != 0 {
		return nil, fmt.Errorf("dosage api code %d: %s", parsed.Code, parsed.Message)
	}
	return &DosageNotify{
		Code: parsed.Data.DosageNotifyCode,
		Zh:   parsed.Data.DosageNotifyZh,
		En:   parsed.Data.DosageNotifyEn,
	}, nil
}

func (c *UpstreamClient) FetchUserResource(ctx context.Context, sessionCookie string) (*model.CreditSnapshot, error) {
	base := global.CORE_CONFIG.CodeBuddy.BillingBaseURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/billing/meter/get-user-resource", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", base)
	req.Header.Set("Referer", strings.TrimRight(base, "/")+"/profile/usage")
	req.Header.Set("Cookie", normalizeSessionCookie(sessionCookie))
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("user-resource http %d: %s", resp.StatusCode, clip(raw, 300))
	}
	snap, err := ParseUserResource(raw)
	if err != nil {
		return nil, err
	}
	return snap, nil
}

func normalizeSessionCookie(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "session=") {
		return raw
	}
	return "session=" + raw
}

func ParseUserResource(raw []byte) (*model.CreditSnapshot, error) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	accounts := findResourceAccounts(root)
	if len(accounts) == 0 {
		return nil, fmt.Errorf("user-resource: no accounts in response")
	}
	snap := &model.CreditSnapshot{}
	packages := make([]map[string]any, 0, len(accounts))
	for _, acc := range accounts {
		typ := asInt(acc["CapacityType"])
		remain := firstFloat(acc["CycleCapacityRemainPrecise"], acc["CapacityRemainPrecise"], acc["CycleCapacityRemain"], acc["CapacityRemain"])
		total := firstFloat(acc["CycleCapacitySize"], acc["CapacitySizePrecise"], acc["CapacitySize"])
		item := map[string]any{
			"package_name":  fmt.Sprint(acc["PackageName"]),
			"capacity_type": typ,
			"total":         total,
			"remain":        remain,
			"cycle_start":   fmt.Sprint(acc["CycleStartTime"]),
			"cycle_end":     fmt.Sprint(acc["CycleEndTime"]),
		}
		packages = append(packages, item)
		if typ == 4 {
			snap.MonthlyTotal += total
			snap.MonthlyRemain += remain
			if snap.CycleStart == nil {
				snap.CycleStart = parseCreditTime(fmt.Sprint(acc["CycleStartTime"]))
			}
			if snap.CycleEnd == nil {
				snap.CycleEnd = parseCreditTime(fmt.Sprint(acc["CycleEndTime"]))
			}
			continue
		}
		snap.OnetimeTotal += total
		snap.OnetimeRemain += remain
	}
	encoded, _ := json.Marshal(packages)
	snap.PackagesJSON = string(encoded)
	return snap, nil
}

func findResourceAccounts(v any) []map[string]any {
	switch n := v.(type) {
	case map[string]any:
		if raw, ok := n["Accounts"].([]any); ok {
			out := make([]map[string]any, 0, len(raw))
			for _, item := range raw {
				if m, ok := item.(map[string]any); ok {
					out = append(out, m)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
		for _, key := range []string{"data", "Data", "Response"} {
			if child, ok := n[key]; ok {
				if found := findResourceAccounts(child); len(found) > 0 {
					return found
				}
			}
		}
	case []any:
		for _, item := range n {
			if found := findResourceAccounts(item); len(found) > 0 {
				return found
			}
		}
	}
	return nil
}

func firstFloat(values ...any) float64 {
	for _, v := range values {
		if v == nil {
			continue
		}
		switch n := v.(type) {
		case string:
			s := strings.TrimSpace(n)
			if s == "" || s == "<nil>" {
				continue
			}
			f, err := strconv.ParseFloat(s, 64)
			if err == nil {
				return f
			}
		default:
			f := asFloat(v)
			if f != 0 {
				return f
			}
		}
	}
	return 0
}

func parseCreditTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "<nil>" {
		return nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return &t
		}
	}
	return nil
}
