package model

import (
	"codebuddy-gateway/global"

	"gorm.io/gorm"
)

func AutoMigrate(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&Account{},
		&LLMModel{},
		&UsageLog{},
	); err != nil {
		return err
	}
	return SeedDefaultModels(db)
}

func MustDB() *gorm.DB {
	return global.CORE_DB
}
