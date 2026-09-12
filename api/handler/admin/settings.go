package admin

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"codebuddy-gateway/api/response"
	"codebuddy-gateway/config"
	"codebuddy-gateway/global"
	"codebuddy-gateway/model"
	"codebuddy-gateway/task"

	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
)

var refreshSettingsMu sync.Mutex

type refreshSettings struct {
	Enabled        bool       `json:"enabled"`
	Cron           string     `json:"cron"`
	ThresholdDays  int        `json:"threshold_days"`
	TimeoutSeconds int        `json:"timeout_seconds"`
	LastRefreshAt  *time.Time `json:"last_refresh_at"`
	NextRunAt      *time.Time `json:"next_run_at"`
}

type refreshSettingsReq struct {
	Enabled        *bool   `json:"enabled"`
	Cron           *string `json:"cron"`
	ThresholdDays  *int    `json:"threshold_days"`
	TimeoutSeconds *int    `json:"timeout_seconds"`
}

func GetRefreshSettings(c *gin.Context) {
	response.Success(c, currentRefreshSettings())
}

func UpdateRefreshSettings(c *gin.Context) {
	var req refreshSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	refreshSettingsMu.Lock()
	defer refreshSettingsMu.Unlock()

	cfg := global.CORE_CONFIG.Refresh
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	if req.Cron != nil {
		spec := strings.TrimSpace(*req.Cron)
		if spec == "" {
			spec = "0 3 * * *"
		}
		if err := validateCronSpec(spec); err != nil {
			response.BadRequest(c, "invalid cron: "+err.Error())
			return
		}
		cfg.Cron = spec
	}
	if req.ThresholdDays != nil {
		if *req.ThresholdDays <= 0 {
			response.BadRequest(c, "threshold_days must be > 0")
			return
		}
		cfg.ThresholdDays = *req.ThresholdDays
	}
	if req.TimeoutSeconds != nil {
		if *req.TimeoutSeconds <= 0 {
			response.BadRequest(c, "timeout_seconds must be > 0")
			return
		}
		cfg.TimeoutSeconds = *req.TimeoutSeconds
	}

	if err := persistRefreshConfig(cfg); err != nil {
		response.Fail(c, err.Error())
		return
	}
	global.CORE_CONFIG.Refresh = cfg
	if err := task.UpdateRefreshSchedule(); err != nil {
		response.Fail(c, "saved but failed to reschedule: "+err.Error())
		return
	}
	response.Success(c, currentRefreshSettings())
}

func currentRefreshSettings() refreshSettings {
	cfg := global.CORE_CONFIG.Refresh
	out := refreshSettings{
		Enabled:        cfg.Enabled,
		Cron:           cfg.Spec(),
		ThresholdDays:  cfg.Threshold(),
		TimeoutSeconds: cfg.Timeout(),
		LastRefreshAt:  latestAccountRefresh(),
	}
	if cfg.Enabled {
		out.NextRunAt = nextCronRun(cfg.Spec())
	}
	return out
}

func latestAccountRefresh() *time.Time {
	list, err := model.ListAccounts()
	if err != nil {
		return nil
	}
	var latest *time.Time
	for _, acc := range list {
		if acc.LastRefresh == nil {
			continue
		}
		if latest == nil || acc.LastRefresh.After(*latest) {
			t := *acc.LastRefresh
			latest = &t
		}
	}
	return latest
}

func nextCronRun(spec string) *time.Time {
	sched, err := cron.ParseStandard(spec)
	if err != nil {
		return nil
	}
	next := sched.Next(time.Now())
	if next.IsZero() {
		return nil
	}
	return &next
}

func validateCronSpec(spec string) error {
	_, err := cron.ParseStandard(spec)
	return err
}

func persistRefreshConfig(cfg config.Refresh) error {
	if global.CORE_VP != nil {
		global.CORE_VP.Set("refresh.enabled", cfg.Enabled)
		global.CORE_VP.Set("refresh.cron", cfg.Cron)
		global.CORE_VP.Set("refresh.threshold-days", cfg.ThresholdDays)
		global.CORE_VP.Set("refresh.timeout-seconds", cfg.TimeoutSeconds)
	}
	body := fmtRefreshBody(cfg)
	return writeYAMLSection("refresh", body)
}

func fmtRefreshBody(cfg config.Refresh) string {
	return "    enabled: " + boolString(cfg.Enabled) + "\n" +
		"    cron: \"" + cfg.Spec() + "\"\n" +
		"    threshold-days: " + strconv.Itoa(cfg.Threshold()) + "\n" +
		"    timeout-seconds: " + strconv.Itoa(cfg.Timeout()) + "\n"
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
