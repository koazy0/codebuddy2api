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
	if err := backfillManuallyDisabled(db); err != nil {
		return err
	}
	return SeedDefaultModels(db)
}

// backfillManuallyDisabled 给历史遗留的「已停用」账号补上人工停用标记。
//
// 为什么需要：manually_disabled 是后加的字段，加之前所有 disabled 都来自
// 面板的手动停用（自动逻辑只用 cooldown）。若不回填，这些账号的标记为空，
// 自动逻辑就分不清「人停用的」和「系统停用的」，未来放宽判定时可能把它们
// 擅自启回来——那正是这个标记要防的事。
//
// 只跑一次：回填后标记非空，条件自然不再命中。已经有标记的行不动，
// 避免覆盖运行时的新状态。
func backfillManuallyDisabled(db *gorm.DB) error {
	return db.Model(&Account{}).
		Where("status = ? AND manually_disabled = ?", AccountStatusDisabled, false).
		Update("manually_disabled", true).Error
}

func MustDB() *gorm.DB {
	return global.CORE_DB
}
