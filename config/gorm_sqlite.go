package config

type Sqlite struct {
	Path         string `mapstructure:"path" json:"path" yaml:"path"`                               // 数据库文件路径
	Prefix       string `mapstructure:"prefix" json:"prefix" yaml:"prefix"`                         // 表名前缀
	Singular     bool   `mapstructure:"singular" json:"singular" yaml:"singular"`                   // 是否使用单数表名
	LogMode      string `mapstructure:"log-mode" json:"log-mode" yaml:"log-mode" default:"silent"`  // 日志模式
	LogZap       bool   `mapstructure:"log-zap" json:"log-zap" yaml:"log-zap"`                      // 是否使用Zap记录日志
	MaxIdleConns int    `mapstructure:"max-idle-conns" json:"max-idle-conns" yaml:"max-idle-conns"` // 空闲中的最大连接数
	MaxOpenConns int    `mapstructure:"max-open-conns" json:"max-open-conns" yaml:"max-open-conns"` // 打开到数据库的最大连接数
}
