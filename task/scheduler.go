package task

import (
	"context"
	"sync"
	"time"

	"codebuddy-gateway/global"
	"codebuddy-gateway/service"
	"codebuddy-gateway/utils/timer"

	"go.uber.org/zap"
)

const (
	refreshCronName = "codebuddy"
	refreshTaskName = "refresh-credentials"
)

var (
	cronTimer timer.Timer
	cronMu    sync.Mutex
)

func Start() {
	cfg := global.CORE_CONFIG
	if cfg.Refresh.Enabled {
		go func() {
			time.Sleep(2 * time.Second)
			service.DefaultRefresher.RefreshDueAccounts(context.Background())
		}()
		if err := UpdateRefreshSchedule(); err != nil {
			global.CORE_LOG.Error("register refresh cron failed", zap.Error(err), zap.String("spec", cfg.Refresh.Spec()))
		} else {
			global.CORE_LOG.Info("credential refresh cron started", zap.String("spec", cfg.Refresh.Spec()))
		}
	}

	if cfg.Watchdog.Enabled {
		go func() {
			time.Sleep(3 * time.Second)
			service.DefaultWatchdog.RunOnce(context.Background())
			ticker := time.NewTicker(time.Duration(cfg.Watchdog.Interval()) * time.Second)
			for range ticker.C {
				service.DefaultWatchdog.RunOnce(context.Background())
			}
		}()
		global.CORE_LOG.Info("watchdog started", zap.Int("interval_seconds", cfg.Watchdog.Interval()))
	}
}

func Stop() {
	cronMu.Lock()
	defer cronMu.Unlock()
	if cronTimer != nil {
		cronTimer.Close()
		cronTimer = nil
	}
}

func UpdateRefreshSchedule() error {
	cronMu.Lock()
	defer cronMu.Unlock()

	cfg := global.CORE_CONFIG.Refresh
	if cronTimer != nil {
		cronTimer.Clear(refreshCronName)
	}
	if !cfg.Enabled {
		global.CORE_LOG.Info("credential refresh cron disabled")
		return nil
	}
	if cronTimer == nil {
		cronTimer = timer.NewTimerTask()
	}
	spec := cfg.Spec()
	_, err := cronTimer.AddTaskByFunc(refreshCronName, spec, func() {
		service.DefaultRefresher.RefreshDueAccounts(context.Background())
	}, refreshTaskName)
	if err != nil {
		return err
	}
	global.CORE_LOG.Info("credential refresh cron updated", zap.String("spec", spec))
	return nil
}
