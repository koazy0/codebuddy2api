package model

import (
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

type UsageLog struct {
	ID                       uint      `gorm:"primarykey" json:"id"`
	AccountID                uint      `gorm:"index" json:"account_id"`
	AccountName              string    `gorm:"size:100;index" json:"account_name"`
	Protocol                 string    `gorm:"size:20;index" json:"protocol"`
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
	ClientIP                 string    `gorm:"size:64" json:"client_ip"`
	UserAgent                string    `gorm:"size:255" json:"user_agent"`
	RequestTokens            int       `json:"request_tokens"`
	RequestPreview           string    `gorm:"type:text" json:"request_preview"`
	ResponseTokens           int       `json:"response_tokens"`
	ResponsePreview          string    `gorm:"type:text" json:"response_preview"`
	RawUsage                 string    `gorm:"type:text" json:"raw_usage"`
	CreatedAt                time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}

func (UsageLog) TableName() string { return "usage_logs" }

type UsageFilter struct {
	AccountID uint
	Model     string
	Protocol  string
	Status    string
	Keyword   string
	Start     *time.Time
	End       *time.Time
	Limit     int
	Offset    int
}

func CreateUsageLog(log *UsageLog) error {
	return MustDB().Create(log).Error
}

func GetUsageLog(id uint) (*UsageLog, error) {
	var item UsageLog
	if err := MustDB().First(&item, id).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

func DeleteUsageLog(id uint) error {
	return MustDB().Delete(&UsageLog{}, id).Error
}

func applyUsageFilter(db *gorm.DB, filter UsageFilter) *gorm.DB {
	if filter.AccountID > 0 {
		db = db.Where("account_id = ?", filter.AccountID)
	}
	if filter.Model != "" {
		db = db.Where("model = ?", filter.Model)
	}
	if filter.Protocol != "" {
		db = db.Where("protocol = ?", filter.Protocol)
	}
	switch strings.ToLower(strings.TrimSpace(filter.Status)) {
	case "":
	case "ok", "success", "200":
		db = db.Where("status_code = ?", 200)
	case "error", "fail", "failed":
		db = db.Where("status_code <> ?", 200)
	default:
		if n, err := strconv.Atoi(filter.Status); err == nil && n > 0 {
			db = db.Where("status_code = ?", n)
		}
	}
	if kw := strings.TrimSpace(filter.Keyword); kw != "" {
		like := "%" + kw + "%"
		db = db.Where("request_preview LIKE ? OR response_preview LIKE ? OR error LIKE ? OR request_id LIKE ? OR account_name LIKE ? OR model LIKE ?",
			like, like, like, like, like, like)
	}
	if filter.Start != nil {
		db = db.Where("created_at >= ?", filter.Start)
	}
	if filter.End != nil {
		db = db.Where("created_at <= ?", filter.End)
	}
	return db
}

func ListUsageLogs(filter UsageFilter) ([]UsageLog, int64, error) {
	db := applyUsageFilter(MustDB().Model(&UsageLog{}), filter)
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	var list []UsageLog
	// 列表不带正文。Codex 系统提示一条就能 16KB，20 条打进控制台会把登录后的
	// Promise.all 卡在 JSON 解析上，表现为「点了进得去、密码也输不了」。
	err := db.Omit("request_preview", "response_preview", "raw_usage").
		Order("id desc").Limit(filter.Limit).Offset(filter.Offset).Find(&list).Error
	return list, total, err
}

func DeleteUsageLogs(filter UsageFilter) (int64, error) {
	tx := applyUsageFilter(MustDB(), filter).Delete(&UsageLog{})
	return tx.RowsAffected, tx.Error
}

func ClearUsageLogs() (int64, error) {
	tx := MustDB().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&UsageLog{})
	return tx.RowsAffected, tx.Error
}

func ListUsageModels() ([]string, error) {
	var list []string
	err := MustDB().Model(&UsageLog{}).
		Where("model <> ''").
		Distinct("model").
		Order("model asc").
		Pluck("model", &list).Error
	return list, err
}

func UsageSummary() (map[string]any, error) {
	type row struct {
		Requests         int64
		ErrorRequests    int64
		PromptTokens     int64
		CompletionTokens int64
		ThinkingTokens   int64
		CacheHitTokens   int64
		CacheMissTokens  int64
		Credit           float64
		AvgLatencyMs     float64
		AvgFirstTokenMs  float64
		AvgTPS           float64
		AvgCacheHitRate  float64
	}
	var r row
	err := MustDB().Model(&UsageLog{}).
		Select(`COUNT(*) as requests,
			COALESCE(SUM(CASE WHEN status_code <> 200 THEN 1 ELSE 0 END),0) as error_requests,
			COALESCE(SUM(prompt_tokens),0) as prompt_tokens,
			COALESCE(SUM(completion_tokens),0) as completion_tokens,
			COALESCE(SUM(thinking_tokens),0) as thinking_tokens,
			COALESCE(SUM(cache_hit_tokens),0) as cache_hit_tokens,
			COALESCE(SUM(cache_miss_tokens),0) as cache_miss_tokens,
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
		"error_requests":        r.ErrorRequests,
		"prompt_tokens":         r.PromptTokens,
		"completion_tokens":     r.CompletionTokens,
		"thinking_tokens":       r.ThinkingTokens,
		"cache_hit_tokens":      r.CacheHitTokens,
		"cache_miss_tokens":     r.CacheMissTokens,
		"credit":                r.Credit,
		"avg_latency_ms":        r.AvgLatencyMs,
		"avg_first_token_ms":    r.AvgFirstTokenMs,
		"avg_tokens_per_second": r.AvgTPS,
		"avg_cache_hit_rate":    r.AvgCacheHitRate,
	}, nil
}

type UsageDailyRow struct {
	Day              string  `json:"day"`
	Requests         int64   `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CacheHitTokens   int64   `json:"cache_hit_tokens"`
	Credit           float64 `json:"credit"`
}

func UsageDaily(days int) ([]UsageDailyRow, error) {
	if days <= 0 || days > 90 {
		days = 14
	}
	start := time.Now().AddDate(0, 0, -days+1).Truncate(24 * time.Hour)
	var rows []UsageDailyRow
	err := MustDB().Model(&UsageLog{}).
		Select(`DATE(created_at) as day,
			COUNT(*) as requests,
			COALESCE(SUM(prompt_tokens),0) as prompt_tokens,
			COALESCE(SUM(completion_tokens),0) as completion_tokens,
			COALESCE(SUM(cache_hit_tokens),0) as cache_hit_tokens,
			COALESCE(SUM(credit),0) as credit`).
		Where("created_at >= ?", start).
		Group("DATE(created_at)").
		Order("day asc").
		Scan(&rows).Error
	return rows, err
}

type UsageGroupRow struct {
	AccountID        uint    `json:"account_id"`
	AccountName      string  `json:"account_name"`
	Model            string  `json:"model"`
	Requests         int64   `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	Credit           float64 `json:"credit"`
}

func UsageByModel(limit int) ([]UsageGroupRow, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	var rows []UsageGroupRow
	err := MustDB().Model(&UsageLog{}).
		Select(`model,
			COUNT(*) as requests,
			COALESCE(SUM(prompt_tokens),0) as prompt_tokens,
			COALESCE(SUM(completion_tokens),0) as completion_tokens,
			COALESCE(SUM(credit),0) as credit`).
		Where("model <> ''").
		Group("model").
		Order("requests desc").
		Limit(limit).
		Scan(&rows).Error
	return rows, err
}

func UsageByAccount(limit int) ([]UsageGroupRow, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var rows []UsageGroupRow
	err := MustDB().Model(&UsageLog{}).
		Select(`account_id,
			account_name,
			COUNT(*) as requests,
			COALESCE(SUM(prompt_tokens),0) as prompt_tokens,
			COALESCE(SUM(completion_tokens),0) as completion_tokens,
			COALESCE(SUM(credit),0) as credit`).
		Group("account_id, account_name").
		Order("requests desc").
		Limit(limit).
		Scan(&rows).Error
	return rows, err
}
