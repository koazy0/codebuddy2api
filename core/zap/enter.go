package zap

import (
	"fmt"
	"os"

	"codebuddy-gateway/global"
	"codebuddy-gateway/utils"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Init 初始化 Zap 日志，对外唯一入口
func Init() *zap.Logger {
	if ok, _ := utils.PathExists(global.CORE_CONFIG.Zap.Director); !ok {
		fmt.Printf("create %v directory\n", global.CORE_CONFIG.Zap.Director)
		_ = os.Mkdir(global.CORE_CONFIG.Zap.Director, os.ModePerm)
	}

	levels := global.CORE_CONFIG.Zap.Levels()
	cores := make([]zapcore.Core, 0, len(levels))
	for i := range levels {
		cores = append(cores, newZapCore(levels[i]))
	}

	logger := zap.New(zapcore.NewTee(cores...))
	if global.CORE_CONFIG.Zap.ShowLine {
		logger = logger.WithOptions(zap.AddCaller())
	}
	return logger
}
