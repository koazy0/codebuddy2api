package command

import (
	"os"
	"os/signal"
	"syscall"

	"codebuddy-gateway/api"
	"codebuddy-gateway/core"
	"codebuddy-gateway/global"
	"codebuddy-gateway/model"
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
	configFile, _ := cmd.Flags().GetString("config")
	apiKey, _ := cmd.Flags().GetString("api-key")
	adminKey, _ := cmd.Flags().GetString("admin-key")

	global.CORE_VP = core.Viper(configFile)
	core.ApplyKeyOverrides(apiKey, adminKey)
	global.CORE_LOG = core.Zap()
	global.CORE_DB = core.InitDB()

	if err := model.AutoMigrate(global.CORE_DB); err != nil {
		global.CORE_LOG.Fatal("database migrate failed: " + err.Error())
	}

	service.InitRuntime()
	global.CORE_LOG.Info("gateway keys loaded",
		zap.String("api_key", service.MaskToken(global.CORE_CONFIG.Gateway.APIKey)),
		zap.String("listen", global.CORE_CONFIG.System.ListenAddr),
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
