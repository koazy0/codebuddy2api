package model

import (
	"errors"

	"gorm.io/gorm"
)

type LLMModel struct {
	ID          uint   `gorm:"primarykey" json:"id"`
	ModelID     string `gorm:"size:100;uniqueIndex;not null" json:"model_id"`
	DisplayName string `gorm:"size:200" json:"display_name"`
	MaxInput    int    `json:"max_input"`
	MaxOutput   int    `json:"max_output"`
	Vision      bool   `json:"vision"`
	Reasoning   bool   `json:"reasoning"`
	Enabled     bool   `gorm:"default:true" json:"enabled"`
	Sort        int    `gorm:"default:0" json:"sort"`
	Remark      string `gorm:"size:500" json:"remark"`
}

func (LLMModel) TableName() string { return "llm_models" }

func SeedDefaultModels(db *gorm.DB) error {
	var count int64
	if err := db.Model(&LLMModel{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	seeds := []LLMModel{
		{ModelID: "auto", DisplayName: "Auto", MaxInput: 168000, MaxOutput: 32000, Vision: true, Enabled: true, Sort: 10},
		{ModelID: "hy3", DisplayName: "Hy3", MaxInput: 192000, MaxOutput: 64000, Reasoning: true, Enabled: true, Sort: 20},
		{ModelID: "hy3-preview", DisplayName: "Hy3 preview", MaxInput: 192000, MaxOutput: 64000, Reasoning: true, Enabled: true, Sort: 30},
		{ModelID: "glm-5.2", DisplayName: "GLM-5.2", MaxInput: 200000, MaxOutput: 48000, Reasoning: true, Enabled: true, Sort: 40},
		{ModelID: "glm-5.1", DisplayName: "GLM-5.1", MaxInput: 200000, MaxOutput: 48000, Reasoning: true, Enabled: true, Sort: 50},
		{ModelID: "glm-5.0-turbo", DisplayName: "GLM-5.0-Turbo", MaxInput: 200000, MaxOutput: 48000, Reasoning: true, Enabled: true, Sort: 60},
		{ModelID: "glm-5v-turbo", DisplayName: "GLM-5V-Turbo", MaxInput: 200000, MaxOutput: 48000, Vision: true, Reasoning: true, Enabled: true, Sort: 70},
		{ModelID: "glm-4.7", DisplayName: "GLM-4.7", MaxInput: 200000, MaxOutput: 48000, Vision: true, Reasoning: true, Enabled: true, Sort: 80},
		{ModelID: "kimi-k3", DisplayName: "Kimi-K3", MaxInput: 256000, MaxOutput: 32000, Vision: true, Reasoning: true, Enabled: true, Sort: 90},
		{ModelID: "kimi-k2.7-code", DisplayName: "Kimi-K2.7-Code", MaxInput: 256000, MaxOutput: 32000, Vision: true, Reasoning: true, Enabled: true, Sort: 100},
		{ModelID: "kimi-k2.6", DisplayName: "Kimi-K2.6", MaxInput: 256000, MaxOutput: 32000, Vision: true, Reasoning: true, Enabled: true, Sort: 110},
		{ModelID: "deepseek-v4-pro", DisplayName: "DeepSeek-V4-Pro", MaxInput: 128000, MaxOutput: 32000, Vision: true, Reasoning: true, Enabled: true, Sort: 120},
		{ModelID: "deepseek-v4-flash", DisplayName: "DeepSeek-V4-Flash", MaxInput: 128000, MaxOutput: 32000, Vision: true, Enabled: true, Sort: 130},
		{ModelID: "deepseek-v3-2-volc", DisplayName: "DeepSeek-V3.2", MaxInput: 96000, MaxOutput: 32000, Vision: true, Reasoning: true, Enabled: true, Sort: 140},
		{ModelID: "deepseek-r1-0528-lkeap", DisplayName: "DeepSeek-R1-0528", MaxInput: 112000, MaxOutput: 16000, Enabled: true, Sort: 150},
		{ModelID: "minimax-m3", DisplayName: "MiniMax-M3", MaxInput: 200000, MaxOutput: 48000, Vision: true, Reasoning: true, Enabled: true, Sort: 160},
		{ModelID: "minimax-m2.7", DisplayName: "MiniMax-M2.7", MaxInput: 200000, MaxOutput: 48000, Vision: true, Reasoning: true, Enabled: true, Sort: 170},
		{ModelID: "hunyuan-chat", DisplayName: "Hunyuan-Turbos", MaxInput: 128000, MaxOutput: 8000, Enabled: true, Sort: 180},
		{ModelID: "codewise-completions", DisplayName: "Codewise Completions", MaxOutput: 256, Enabled: true, Sort: 190},
	}
	return db.Create(&seeds).Error
}

func ListEnabledModels() ([]LLMModel, error) {
	var list []LLMModel
	err := MustDB().Where("enabled = ?", true).Order("sort asc, id asc").Find(&list).Error
	return list, err
}

func ListModels() ([]LLMModel, error) {
	var list []LLMModel
	err := MustDB().Order("sort asc, id asc").Find(&list).Error
	return list, err
}

func UpsertModel(m *LLMModel) error {
	var existing LLMModel
	err := MustDB().Where("model_id = ?", m.ModelID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MustDB().Create(m).Error
	}
	if err != nil {
		return err
	}
	m.ID = existing.ID
	return MustDB().Save(m).Error
}

func SetModelEnabled(id uint, enabled bool) error {
	return MustDB().Model(&LLMModel{}).Where("id = ?", id).Update("enabled", enabled).Error
}
