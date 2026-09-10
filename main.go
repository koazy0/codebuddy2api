package main

import (
	"fmt"

	"codebuddy-gateway/cli"
	"codebuddy-gateway/core"
	"codebuddy-gateway/global"
	"codebuddy-gateway/model"
	"codebuddy-gateway/service"
)

var Version = "v1.0.0"
var AppName = "CodeBuddyGateway"

func main() {
	fmt.Println("CodeBuddy Gateway\n Version: ", Version)
	global.CORE_APP_NAME = AppName
	global.CORE_APP_VERSION = Version
	global.CORE_VP = core.Viper()
	global.CORE_LOG = core.Zap()
	global.CORE_DB = core.InitDB()

	if err := model.AutoMigrate(global.CORE_DB); err != nil {
		global.CORE_LOG.Fatal("database migrate failed: " + err.Error())
	}

	service.InitRuntime()
	cli.Execute()
}
