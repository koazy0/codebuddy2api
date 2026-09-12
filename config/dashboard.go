package config

import "strings"

// Dashboard 控制台的登录方式：可以只用 admin-key，也可以单独设面板密码，
// 两者都配则同时校验。密码落盘时会写成 sha256: 摘要。
type Dashboard struct {
	Title               string `mapstructure:"title" json:"title" yaml:"title"`
	Password            string `mapstructure:"password" json:"password" yaml:"password"`
	RequireAdminKeyFlag *bool  `mapstructure:"require-admin-key" json:"require-admin-key" yaml:"require-admin-key"`
}

func (d Dashboard) TitleOrDefault() string {
	if strings.TrimSpace(d.Title) == "" {
		return "CodeBuddy2API 控制台"
	}
	return strings.TrimSpace(d.Title)
}

// RequireAdminKey 默认开启，只有显式写成 false 才关掉。
func (d Dashboard) RequireAdminKey() bool {
	if d.RequireAdminKeyFlag == nil {
		return true
	}
	return *d.RequireAdminKeyFlag
}
