package db

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

	db, err := gorm.Open(sqlite.Open(sqliteDSN(cfg.Path)), &gorm.Config{
		Logger:                                   logger.Default.LogMode(getLogMode(cfg.LogMode)),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		panic(fmt.Sprintf("SQLite 连接失败: %v", err))
	}

	sqlDB, _ := db.DB()
	// SQLite 不是多连接数据库。DELETE 日志 + MaxOpenConns=100 会在
	// 控制台 10 路并发读和看门狗/用量写入之间互相卡住，页面表现为登录后假死。
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	return db
}

func sqliteDSN(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "data/gateway.db"
	}
	if strings.Contains(path, "?") || strings.HasPrefix(path, "file:") {
		return path
	}
	return "file:" + path + "?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&cache=shared"
}
