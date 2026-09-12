package command

import (
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"codebuddy-gateway/api"
	"codebuddy-gateway/core"
	"codebuddy-gateway/global"
	"codebuddy-gateway/service"
	"codebuddy-gateway/task"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func NewServerCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "server",
		Short: "Start the CodeBuddy gateway",
		Run:   ServerCommandFunc,
	}
}

func ServerCommandFunc(cmd *cobra.Command, args []string) {
	Bootstrap(cmd)
	service.InitRuntime()
	// Codex/SSH 的 PTY 关掉时会给当前进程组发 SIGHUP。忽略它，避免「动一动进程就没了」。
	signal.Ignore(syscall.SIGHUP)
	writePIDFile()
	global.CORE_LOG.Info("gateway keys loaded",
		zap.String("api_key", service.MaskToken(global.CORE_CONFIG.Gateway.APIKey)),
		zap.String("listen", global.CORE_CONFIG.System.ListenAddr),
		zap.Bool("dev", global.CORE_DEV),
	)

	app := api.NewAPIServer()
	task.Start()
	go app.ServerRun()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	global.CORE_LOG.Info("received signal, shutting down: " + sig.String())

	task.Stop()
	app.ServerShutdown()
	core.CloseDB()
}

func writePIDFile() {
	path := strings.TrimSpace(os.Getenv("GATEWAY_PID_FILE"))
	if path == "" {
		return
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		global.CORE_LOG.Warn("write pid file failed", zap.String("path", path), zap.Error(err))
	}
}
