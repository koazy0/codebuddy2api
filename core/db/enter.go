package db

import (
	"fmt"
	"sync"

	"codebuddy-gateway/global"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Database 数据库接口
type Database interface {
	Connect() *gorm.DB
}

var (
	dbs      = make(map[string]Database)
	once     sync.Once
	instance *gorm.DB
)

// Register 注册数据库驱动
func Register(db Database, name ...string) {
	key := "default"
	if len(name) > 0 {
		key = name[0]
	}
	dbs[key] = db
}

// NewPgsql 创建 PostgreSQL 实例
func NewPgsql() Database {
	return &Pgsql{}
}

// NewSqlite 创建 SQLite 实例
func NewSqlite() Database {
	return &Sqlite{}
}

// NewMysql 创建 MySQL 实例
func NewMysql() Database {
	return &Mysql{}
}

// Init 初始化数据库
func Init() *gorm.DB {
	once.Do(func() {
		dbType := global.CORE_CONFIG.System.Db
		db, ok := dbs["default"]
		if !ok {
			panic(fmt.Sprintf("数据库驱动 %s 未注册", dbType))
		}
		instance = db.Connect()
		if instance == nil {
			panic(fmt.Sprintf("数据库 %s 连接失败", dbType))
		}
		global.CORE_LOG.Info("数据库连接成功", zap.String("type", dbType))
	})
	return instance
}

// GetDB 获取数据库实例
func GetDB() *gorm.DB {
	if instance == nil {
		return Init()
	}
	return instance
}

// getLogMode 获取日志模式
func getLogMode(mode string) logger.LogLevel {
	switch mode {
	case "silent":
		return logger.Silent
	case "error":
		return logger.Error
	case "warn":
		return logger.Warn
	case "info":
		return logger.Info
	default:
		return logger.Info
	}
}
