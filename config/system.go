package config

type System struct {
	Db         string `mapstructure:"db" json:"db" yaml:"db"`                         // 数据库类型: sqlite, pgsql, mysql
	ListenAddr string `mapstructure:"listenAddr" json:"listenAddr" yaml:"listenAddr"` // 监听地址
}
