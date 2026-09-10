package command

import (
	"os"
	"os/signal"
	"syscall"

	"codebuddy-gateway/api"
	"codebuddy-gateway/core"
	"codebuddy-gateway/global"
	"codebuddy-gateway/task"

	"github.com/spf13/cobra"
)

func NewServerCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "server",
		Short: "Start the CodeBuddy gateway",
		Run:   ServerCommandFunc,
	}
}

func ServerCommandFunc(cmd *cobra.Command, args []string) {
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
