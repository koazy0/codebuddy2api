package model

import "time"

type UsageLog struct {
	ID                       uint      `gorm:"primarykey" json:"id"`
	AccountID                uint      `gorm:"index" json:"account_id"`
	Model                    string    `gorm:"size:100;index" json:"model"`
	UpstreamModel            string    `gorm:"size:100" json:"upstream_model"`
	Stream                   bool      `json:"stream"`
	PromptTokens             int       `json:"prompt_tokens"`
	CompletionTokens         int       `json:"completion_tokens"`
	TotalTokens              int       `json:"total_tokens"`
	ThinkingTokens           int       `json:"thinking_tokens"`
	Credit                   float64   `json:"credit"`
	EstimatedCredit          float64   `json:"estimated_credit"`
	CreditMonthly            float64   `json:"credit_monthly"`
	CreditOnetime            float64   `json:"credit_onetime"`
	CreditSource             string    `gorm:"size:20" json:"credit_source"`
	CacheHitTokens           int       `json:"cache_hit_tokens"`
	CacheMissTokens          int       `json:"cache_miss_tokens"`
	CacheHitRate             float64   `json:"cache_hit_rate"`
	CacheReadInputTokens     int       `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int       `json:"cache_creation_input_tokens"`
	CacheWriteTokens         int       `json:"cache_write_tokens"`
	CachedTokens             int       `json:"cached_tokens"`
	FirstTokenMs             int64     `json:"first_token_ms"`
	LatencyMs                int64     `json:"latency_ms"`
	TokensPerSecond          float64   `json:"tokens_per_second"`
	OutputTokensPerSecond    float64   `json:"output_tokens_per_second"`
	StatusCode               int       `json:"status_code"`
	Error                    string    `gorm:"type:text" json:"error"`
	RequestID                string    `gorm:"size:64;index" json:"request_id"`
	CreatedAt                time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}

func (UsageLog) TableName() string { return "usage_logs" }

func CreateUsageLog(log *UsageLog) error {
	return MustDB().Create(log).Error
}

func ListUsageLogs(accountID uint, model string, limit, offset int) ([]UsageLog, int64, error) {
	db := MustDB().Model(&UsageLog{})
	if accountID > 0 {
		db = db.Where("account_id = ?", accountID)
	}
	if model != "" {
		db = db.Where("model = ?", model)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []UsageLog
	err := db.Order("id desc").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

func UsageSummary() (map[string]any, error) {
	type row struct {
		Requests         int64
		PromptTokens     int64
		CompletionTokens int64
		ThinkingTokens   int64
		Credit           float64
		AvgLatencyMs     float64
		AvgFirstTokenMs  float64
		AvgTPS           float64
		AvgCacheHitRate  float64
	}
	var r row
	err := MustDB().Model(&UsageLog{}).
		Select(`COUNT(*) as requests,
			COALESCE(SUM(prompt_tokens),0) as prompt_tokens,
			COALESCE(SUM(completion_tokens),0) as completion_tokens,
			COALESCE(SUM(thinking_tokens),0) as thinking_tokens,
			COALESCE(SUM(credit),0) as credit,
			COALESCE(AVG(CASE WHEN status_code = 200 THEN latency_ms END),0) as avg_latency_ms,
			COALESCE(AVG(CASE WHEN status_code = 200 AND first_token_ms > 0 THEN first_token_ms END),0) as avg_first_token_ms,
			COALESCE(AVG(CASE WHEN status_code = 200 AND tokens_per_second > 0 THEN tokens_per_second END),0) as avg_tps,
			COALESCE(AVG(CASE WHEN status_code = 200 AND cache_hit_rate > 0 THEN cache_hit_rate END),0) as avg_cache_hit_rate`).
		Scan(&r).Error
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"requests":              r.Requests,
		"prompt_tokens":         r.PromptTokens,
		"completion_tokens":     r.CompletionTokens,
		"thinking_tokens":       r.ThinkingTokens,
		"credit":                r.Credit,
		"avg_latency_ms":        r.AvgLatencyMs,
		"avg_first_token_ms":    r.AvgFirstTokenMs,
		"avg_tokens_per_second": r.AvgTPS,
		"avg_cache_hit_rate":    r.AvgCacheHitRate,
	}, nil
}
