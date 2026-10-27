package database

import (
	"context"
	"fmt"

	contractsbinding "github.com/gradii/framework/contracts/binding"
	contractsconsole "github.com/gradii/framework/contracts/console"
	"github.com/gradii/framework/contracts/database/driver"
	"github.com/gradii/framework/contracts/foundation"
	contractstelemetry "github.com/gradii/framework/contracts/telemetry"
	"github.com/gradii/framework/database/console"
	consolemigration "github.com/gradii/framework/database/console/migration"
	"github.com/gradii/framework/database/db"
	databasedriver "github.com/gradii/framework/database/driver"
	"github.com/gradii/framework/database/internal"
	"github.com/gradii/framework/database/migration"
	databaseschema "github.com/gradii/framework/database/schema"
	databaseseeder "github.com/gradii/framework/database/seeder"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/binding"
	"github.com/gradii/framework/support/color"
)

type ServiceProvider struct {
}

func (r *ServiceProvider) Relationship() contractsbinding.Relationship {
	bindings := []string{
		contractsbinding.Orm,
		contractsbinding.DB,
		contractsbinding.Schema,
		contractsbinding.Seeder,
	}

	return contractsbinding.Relationship{
		Bindings:     bindings,
		Dependencies: binding.Dependencies(bindings...),
		ProvideFor:   []string{},
	}
}

func (r *ServiceProvider) Register(app foundation.Application) {
	// Reconnect on a rebuild (e.g. app.Restart) using the current configuration.
	databasedriver.CloseConnections()

	app.Singleton(contractsbinding.Orm, func(app foundation.Application) (any, error) {
		ctx := context.Background()
		config := app.MakeConfig()
		if config == nil {
			return nil, errors.ConfigFacadeNotSet.SetModule(errors.ModuleOrm)
		}

		log := app.MakeLog()
		if log == nil {
			return nil, errors.LogFacadeNotSet.SetModule(errors.ModuleOrm)
		}

		telemetryResolver := telemetryResolverFrom(app)

		connection := config.GetString("database.default")
		if connection == "" {
			return nil, nil
		}

		orm, err := internal.BuildOrm(ctx, config, connection, log, app.Fresh, telemetryResolver)
		if err != nil {
			color.Warningln(errors.OrmInitConnection.Args(connection, err).SetModule(errors.ModuleOrm))

			return orm, nil
		}

		return orm, nil
	})

	app.Singleton(contractsbinding.DB, func(app foundation.Application) (any, error) {
		config := app.MakeConfig()
		if config == nil {
			return nil, errors.ConfigFacadeNotSet.SetModule(errors.ModuleDB)
		}

		log := app.MakeLog()
		if log == nil {
			return nil, errors.LogFacadeNotSet.SetModule(errors.ModuleDB)
		}

		telemetryResolver := telemetryResolverFrom(app)

		connection := config.GetString("database.default")
		if connection == "" {
			return nil, nil
		}

		db, err := db.BuildDB(context.Background(), config, log, connection, telemetryResolver)
		if err != nil {
			color.Warningln(errors.OrmInitConnection.Args(connection, err).SetModule(errors.ModuleDB))

			return nil, nil
		}

		return db, nil
	})

	app.Singleton(contractsbinding.Schema, func(app foundation.Application) (any, error) {
		config := app.MakeConfig()
		if config == nil {
			return nil, errors.ConfigFacadeNotSet.SetModule(errors.ModuleSchema)
		}

		log := app.MakeLog()
		if log == nil {
			return nil, errors.LogFacadeNotSet.SetModule(errors.ModuleSchema)
		}

		orm := app.MakeOrm()
		if orm == nil {
			// The Orm module will print the error message, so it's safe to return a schema with nil values.
			return databaseschema.NewSchema(config, log, nil, nil, nil)
		}

		driverCallback, exist := config.Get(fmt.Sprintf("database.connections.%s.via", orm.Name())).(func() (driver.Driver, error))
		if !exist {
			return nil, errors.DatabaseConfigNotFound
		}

		driver, err := driverCallback()
		if err != nil {
			return nil, err
		}

		connection := app.MakeDB()
		if connection != nil && connection.Name() != orm.Name() {
			connection = connection.Connection(orm.Name())
		}
		return databaseschema.NewSchema(config, log, orm, driver, nil, connection)
	})
	app.Singleton(contractsbinding.Seeder, func(app foundation.Application) (any, error) {
		return databaseseeder.NewSeederFacade(), nil
	})
}

func (r *ServiceProvider) Boot(app foundation.Application) {
	r.registerCommands(app)
}

func (r *ServiceProvider) registerCommands(app foundation.Application) {
	artisan := app.MakeArtisan()
	config := app.MakeConfig()
	log := app.MakeLog()
	schema := app.MakeSchema()
	seeder := app.MakeSeeder()

	if artisan != nil && config != nil && log != nil && schema != nil && seeder != nil {
		migrator := migration.NewMigrator(artisan, schema, config.GetString("database.migrations.table"))
		app.Commands([]contractsconsole.Command{
			consolemigration.NewMigrateMakeCommand(app, migrator),
			consolemigration.NewMigrateCommand(migrator),
			consolemigration.NewMigrateRollbackCommand(migrator),
			consolemigration.NewMigrateResetCommand(migrator),
			consolemigration.NewMigrateRefreshCommand(artisan),
			consolemigration.NewMigrateFreshCommand(artisan, migrator),
			consolemigration.NewMigrateStatusCommand(migrator),
			console.NewModelMakeCommand(artisan, schema),
			console.NewObserverMakeCommand(),
			console.NewSeedCommand(config, seeder),
			console.NewSeederMakeCommand(app),
			console.NewFactoryMakeCommand(),
			console.NewTableCommand(config, schema),
			console.NewShowCommand(config, schema),
			console.NewWipeCommand(config, schema),
		})
	}
}

func telemetryResolverFrom(app foundation.Application) contractstelemetry.Resolver {
	return func() contractstelemetry.Telemetry {
		return app.MakeTelemetry()
	}
}
