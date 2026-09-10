package core

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"codebuddy-gateway/global"

	"github.com/spf13/viper"
)

func Viper(path ...string) *viper.Viper {
	config := "config.yaml"
	if len(path) > 0 && strings.TrimSpace(path[0]) != "" {
		config = strings.TrimSpace(path[0])
	}

	loadDotEnv(".env")

	v := viper.New()
	v.SetConfigFile(config)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		panic(fmt.Errorf("Fatal error config file: %s \n", err))
	}

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	_ = v.BindEnv("gateway.api-key", "GATEWAY_API_KEY", "API_KEY")
	_ = v.BindEnv("gateway.admin-key", "GATEWAY_ADMIN_KEY", "ADMIN_KEY")
	_ = v.BindEnv("system.listenAddr", "GATEWAY_LISTEN", "LISTEN_ADDR")

	if err := v.Unmarshal(&global.CORE_CONFIG); err != nil {
		panic(err)
	}
	return v
}

func ApplyKeyOverrides(apiKey, adminKey string) {
	if v := strings.TrimSpace(apiKey); v != "" {
		global.CORE_CONFIG.Gateway.APIKey = v
	}
	if v := strings.TrimSpace(adminKey); v != "" {
		global.CORE_CONFIG.Gateway.AdminKey = v
	}
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(line[7:])
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, value)
	}
}
