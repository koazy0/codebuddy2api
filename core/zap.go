package core

import (
	zap_internal "codebuddy-gateway/core/zap"

	"go.uber.org/zap"
)

// Zap 初始化日志，委托给 core/zap 包
func Zap() *zap.Logger {
	return zap_internal.Init()
}
