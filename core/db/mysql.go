package db

import (
	"fmt"

	"codebuddy-gateway/global"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Mysql MySQL 数据库
type Mysql struct{}

// Connect 连接 MySQL
func (m *Mysql) Connect() *gorm.DB {
	cfg := global.CORE_CONFIG.Mysql
	dsn := cfg.Dsn()

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger:                                   logger.Default.LogMode(getLogMode(cfg.LogMode)),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		panic(fmt.Sprintf("MySQL 连接失败: %v", err))
	}

	sqlDB, _ := db.DB()
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)

	return db
}
