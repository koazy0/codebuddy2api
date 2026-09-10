package core

import (
	dbInternal "codebuddy-gateway/core/db"
	"codebuddy-gateway/global"

	"gorm.io/gorm"
)

// InitDB 初始化数据库，委托给 core/db 包
func InitDB() *gorm.DB {
	dbType := global.CORE_CONFIG.System.Db

	// 根据配置只注册对应的数据库驱动
	switch dbType {
	case "pgsql":
		dbInternal.Register(dbInternal.NewPgsql())
	case "mysql":
		dbInternal.Register(dbInternal.NewMysql())
	case "sqlite":
		dbInternal.Register(dbInternal.NewSqlite())
	default:
		dbInternal.Register(dbInternal.NewSqlite())
	}

	return dbInternal.Init()
}

// CloseDB 关闭数据库连接
func CloseDB() {
	if global.CORE_DB != nil {
		sqlDB, err := global.CORE_DB.DB()
		if err == nil {
			sqlDB.Close()
		}
	}
}
