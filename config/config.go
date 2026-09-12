package config

type CORE struct {
	Zap       Zap       `mapstructure:"zap" json:"zap" yaml:"zap"`
	System    System    `mapstructure:"system" json:"system" yaml:"system"`
	Pgsql     Pgsql     `mapstructure:"pgsql" json:"pgsql" yaml:"pgsql"`
	Sqlite    Sqlite    `mapstructure:"sqlite" json:"sqlite" yaml:"sqlite"`
	Mysql     Mysql     `mapstructure:"mysql" json:"mysql" yaml:"mysql"`
	Gateway   Gateway   `mapstructure:"gateway" json:"gateway" yaml:"gateway"`
	CodeBuddy CodeBuddy `mapstructure:"codebuddy" json:"codebuddy" yaml:"codebuddy"`
	Refresh   Refresh   `mapstructure:"refresh" json:"refresh" yaml:"refresh"`
	Watchdog  Watchdog  `mapstructure:"watchdog" json:"watchdog" yaml:"watchdog"`
	Dashboard Dashboard `mapstructure:"dashboard" json:"dashboard" yaml:"dashboard"`
}
