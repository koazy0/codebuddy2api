package main

import (
	"fmt"

	"codebuddy-gateway/cli"
	"codebuddy-gateway/global"
)

var Version = "v1.0.0"
var AppName = "CodeBuddyGateway"

func main() {
	fmt.Println("CodeBuddy Gateway\n Version: ", Version)
	global.CORE_APP_NAME = AppName
	global.CORE_APP_VERSION = Version
	cli.Execute()
}
