package db

import (
	"fmt"
	"os"
	"path/filepath"

	"codebuddy-gateway/global"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Sqlite SQLite 数据库
type Sqlite struct{}

// Connect 连接 SQLite
func (s *Sqlite) Connect() *gorm.DB {
	cfg := global.CORE_CONFIG.Sqlite

	// 自动创建数据库目录
	dir := filepath.Dir(cfg.Path)
	if dir != "" && dir != "." {
		os.MkdirAll(dir, os.ModePerm)
	}

	db, err := gorm.Open(sqlite.Open(cfg.Path), &gorm.Config{
		Logger:                                   logger.Default.LogMode(getLogMode(cfg.LogMode)),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		panic(fmt.Sprintf("SQLite 连接失败: %v", err))
	}

	sqlDB, _ := db.DB()
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)

	return db
}
