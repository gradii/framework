package driver

import (
	"context"
	"time"

	gormlogger "gorm.io/gorm/logger"

	contractslogger "github.com/goravel/framework/contracts/database/logger"
	"github.com/goravel/framework/support/carbon"
)

// GormLogger adapts the framework's database logger to the interface GORM requires.
//
// It lives here rather than in contracts/database/logger because GORM is the only consumer: the
// framework's own execution path logs through contractslogger.Logger directly. When GORM goes, this
// file goes with it.
type GormLogger struct {
	logger contractslogger.Logger
}

func NewGormLogger(logger contractslogger.Logger) *GormLogger {
	return &GormLogger{logger: logger}
}

func (r *GormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	_ = r.logger.Level(gormLevelToLevel(level))

	return r
}

func (r *GormLogger) Info(ctx context.Context, msg string, data ...any) {
	r.logger.Infof(ctx, msg, data...)
}

func (r *GormLogger) Warn(ctx context.Context, msg string, data ...any) {
	r.logger.Warningf(ctx, msg, data...)
}

func (r *GormLogger) Error(ctx context.Context, msg string, data ...any) {
	r.logger.Errorf(ctx, msg, data...)
}

func (r *GormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, rowsAffected := fc()

	r.logger.Trace(ctx, carbon.FromStdTime(begin), sql, rowsAffected, err)
}

func gormLevelToLevel(level gormlogger.LogLevel) contractslogger.Level {
	switch level {
	case gormlogger.Silent:
		return contractslogger.Silent
	case gormlogger.Error:
		return contractslogger.Error
	case gormlogger.Warn:
		return contractslogger.Warn
	default:
		return contractslogger.Info
	}
}
