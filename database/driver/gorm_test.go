package driver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
	gormtests "gorm.io/gorm/utils/tests"

	"github.com/goravel/framework/contracts/database"
	contractslogger "github.com/goravel/framework/contracts/database/logger"
	"github.com/goravel/framework/contracts/log"
	contractstelemetry "github.com/goravel/framework/contracts/telemetry"
	mocksconfig "github.com/goravel/framework/mocks/config"
	"github.com/goravel/framework/support/carbon"
	instrumentationdatabase "github.com/goravel/framework/telemetry/instrumentation/database"
)

func TestBuildGorm_TelemetryPlugin(t *testing.T) {
	t.Cleanup(ResetConnections)

	resolver := func() contractstelemetry.Telemetry { return nil }
	instance, _, err := BuildGorm(stubGormConfig(t), discardLogger{}, stubPool(), "primary", resolver)
	assert.NoError(t, err)
	assert.NotNil(t, instance)
	t.Cleanup(func() {
		if db, err := instance.DB(); err == nil {
			_ = db.Close()
		}
	})

	_, registered := instance.Plugins[instrumentationdatabase.PluginName]
	assert.True(t, registered)
}

func TestCloseConnections_ClosesPool(t *testing.T) {
	t.Cleanup(CloseConnections)

	resolver := func() contractstelemetry.Telemetry { return nil }
	instance, _, err := BuildGorm(stubGormConfig(t), discardLogger{}, stubPool(), "primary", resolver)
	assert.NoError(t, err)
	sqlDB, err := instance.DB()
	assert.NoError(t, err)

	CloseConnections()

	assert.ErrorContains(t, sqlDB.Ping(), "database is closed")
}

func TestResetConnections_KeepsPoolOpen(t *testing.T) {
	t.Cleanup(CloseConnections)

	resolver := func() contractstelemetry.Telemetry { return nil }
	first, _, err := BuildGorm(stubGormConfig(t), discardLogger{}, stubPool(), "primary", resolver)
	assert.NoError(t, err)
	sqlDB, err := first.DB()
	assert.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	ResetConnections()

	err = sqlDB.Ping()
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), "database is closed")

	second, _, err := BuildGorm(stubGormConfig(t), discardLogger{}, stubPool(), "primary", resolver)
	assert.NoError(t, err)
	assert.NotSame(t, first, second)
}

func TestResetConnections_NilInstrument(t *testing.T) {
	t.Cleanup(CloseConnections)

	config := mocksconfig.NewConfig(t)
	config.EXPECT().GetInt("database.pool.max_idle_conns", 10).Return(10).Once()
	config.EXPECT().GetInt("database.pool.max_open_conns", 100).Return(100).Once()
	config.EXPECT().GetDuration("database.pool.conn_max_idletime", time.Duration(3600)).Return(time.Duration(3600)).Once()
	config.EXPECT().GetDuration("database.pool.conn_max_lifetime", time.Duration(3600)).Return(time.Duration(3600)).Once()

	_, instrument, err := BuildGorm(config, discardLogger{}, stubPool(), "primary", nil)
	assert.NoError(t, err)
	assert.Nil(t, instrument)

	assert.NotPanics(t, ResetConnections)
}

// discardLogger satisfies the framework's database logger without recording anything, which is what
// these connection tests need: they assert on pool lifecycle, not on log output.
type discardLogger struct{}

func (discardLogger) Level(contractslogger.Level) contractslogger.Logger          { return discardLogger{} }
func (discardLogger) Log() log.Log                                                { return nil }
func (discardLogger) Infof(context.Context, string, ...any)                       {}
func (discardLogger) Warningf(context.Context, string, ...any)                    {}
func (discardLogger) Errorf(context.Context, string, ...any)                      {}
func (discardLogger) Panicf(context.Context, string, ...any)                      {}
func (discardLogger) Trace(context.Context, *carbon.Carbon, string, int64, error) {}

type stubConnector struct{}

func (stubConnector) Connect(context.Context) (driver.Conn, error) { return nil, driver.ErrBadConn }
func (stubConnector) Driver() driver.Driver                        { return stubDriver{} }

type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) { return nil, driver.ErrBadConn }

type stubDialector struct {
	gormtests.DummyDialector
}

func (stubDialector) Initialize(db *gorm.DB) error {
	if err := (gormtests.DummyDialector{}).Initialize(db); err != nil {
		return err
	}

	db.ConnPool = sql.OpenDB(stubConnector{})

	return nil
}

func stubPool() database.Pool {
	return database.Pool{
		Writers: []database.Config{
			{Driver: "postgres", Database: "app", Dialector: stubDialector{}},
		},
	}
}

func stubGormConfig(t *testing.T) *mocksconfig.Config {
	mockConfig := mocksconfig.NewConfig(t)
	mockConfig.EXPECT().GetBool("telemetry.instrumentation.database.enabled", true).Return(true).Once()
	mockConfig.EXPECT().GetInt("database.pool.max_idle_conns", 10).Return(10).Once()
	mockConfig.EXPECT().GetInt("database.pool.max_open_conns", 100).Return(100).Once()
	mockConfig.EXPECT().GetDuration("database.pool.conn_max_idletime", time.Duration(3600)).Return(time.Duration(3600)).Once()
	mockConfig.EXPECT().GetDuration("database.pool.conn_max_lifetime", time.Duration(3600)).Return(time.Duration(3600)).Once()

	return mockConfig
}
