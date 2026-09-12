package command

import (
	"codebuddy-gateway/core"
	"codebuddy-gateway/global"
	"codebuddy-gateway/model"

	"github.com/spf13/cobra"
)

func Bootstrap(cmd *cobra.Command) {
	configFile, _ := cmd.Flags().GetString("config")
	apiKey, _ := cmd.Flags().GetString("api-key")
	adminKey, _ := cmd.Flags().GetString("admin-key")
	dev, _ := cmd.Flags().GetBool("dev")
	global.CORE_DEV = dev

	global.CORE_VP = core.Viper(configFile)
	core.ApplyKeyOverrides(apiKey, adminKey)
	if global.CORE_LOG == nil {
		global.CORE_LOG = core.Zap()
	}
	if global.CORE_DB == nil {
		global.CORE_DB = core.InitDB()
		if err := model.AutoMigrate(global.CORE_DB); err != nil {
			global.CORE_LOG.Fatal("database migrate failed: " + err.Error())
		}
	}
}
