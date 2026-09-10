package zap

import (
	"os"
	"time"

	"codebuddy-gateway/global"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// zapCore 自定义 Zap Core，支持按级别和字段动态路由日志
type zapCore struct {
	level zapcore.Level
	zapcore.Core
}

func newZapCore(level zapcore.Level) *zapCore {
	entity := &zapCore{level: level}
	syncer := entity.writeSyncer()
	levelEnabler := zap.LevelEnablerFunc(func(l zapcore.Level) bool {
		return l == level
	})
	entity.Core = zapcore.NewCore(global.CORE_CONFIG.Zap.Encoder(), syncer, levelEnabler)
	return entity
}

func (z *zapCore) writeSyncer(formats ...string) zapcore.WriteSyncer {
	cutter := newCutter(
		global.CORE_CONFIG.Zap.Director,
		z.level.String(),
		global.CORE_CONFIG.Zap.RetentionDay,
		cutterWithLayout(time.DateOnly),
		cutterWithFormats(formats...),
	)
	if global.CORE_CONFIG.Zap.LogInConsole {
		return zapcore.AddSync(zapcore.NewMultiWriteSyncer(os.Stdout, cutter))
	}
	return zapcore.AddSync(cutter)
}

func (z *zapCore) Enabled(level zapcore.Level) bool {
	return z.level == level
}

func (z *zapCore) With(fields []zapcore.Field) zapcore.Core {
	return z.Core.With(fields)
}

func (z *zapCore) Check(entry zapcore.Entry, check *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if z.Enabled(entry.Level) {
		return check.AddCore(entry, z)
	}
	return check
}

func (z *zapCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	for i := 0; i < len(fields); i++ {
		if fields[i].Key == "business" || fields[i].Key == "folder" || fields[i].Key == "directory" {
			syncer := z.writeSyncer(fields[i].String)
			z.Core = zapcore.NewCore(global.CORE_CONFIG.Zap.Encoder(), syncer, z.level)
		}
	}
	return z.Core.Write(entry, fields)
}

func (z *zapCore) Sync() error {
	return z.Core.Sync()
}
