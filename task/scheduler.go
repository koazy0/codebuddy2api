package task

import (
	"context"
	"time"

	"codebuddy-gateway/global"
	"codebuddy-gateway/service"
	"codebuddy-gateway/utils/timer"

	"go.uber.org/zap"
)

var cronTimer timer.Timer

func Start() {
	cfg := global.CORE_CONFIG
	if cfg.Refresh.Enabled {
		go func() {
			time.Sleep(2 * time.Second)
			service.DefaultRefresher.RefreshDueAccounts(context.Background())
		}()
		cronTimer = timer.NewTimerTask()
		spec := cfg.Refresh.Spec()
		if _, err := cronTimer.AddTaskByFunc("codebuddy", spec, func() {
			service.DefaultRefresher.RefreshDueAccounts(context.Background())
		}, "refresh-credentials"); err != nil {
			global.CORE_LOG.Error("register refresh cron failed", zap.Error(err), zap.String("spec", spec))
		} else {
			global.CORE_LOG.Info("credential refresh cron started", zap.String("spec", spec))
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
	if cronTimer != nil {
		cronTimer.Close()
	}
}
