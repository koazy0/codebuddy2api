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
)
