package global

import (
	"codebuddy-gateway/config"

	"github.com/spf13/viper"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var (
	CORE_APP_NAME    string
	CORE_APP_VERSION string
	CORE_VP          *viper.Viper
	CORE_LOG         *zap.Logger
	CORE_CONFIG      config.CORE
	CORE_DB          *gorm.DB
	// CORE_DEV 为真时控制台前端从 web/ 目录实时读取，刷新浏览器即可生效，
	// 不用重新编译、不用重启进程。
	CORE_DEV bool
)
