package http

import (
	contractsbinding "github.com/gradii/framework/contracts/binding"
	contractsconsole "github.com/gradii/framework/contracts/console"
	"github.com/gradii/framework/contracts/foundation"
	contractstelemetry "github.com/gradii/framework/contracts/telemetry"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/http/client"
	"github.com/gradii/framework/http/console"
	"github.com/gradii/framework/support/binding"
)

type ServiceProvider struct{}

var (
	App foundation.Application
)

func (r *ServiceProvider) Relationship() contractsbinding.Relationship {
	bindings := []string{
		contractsbinding.Http,
		contractsbinding.RateLimiter,
		contractsbinding.View,
	}

	return contractsbinding.Relationship{
		Bindings:     bindings,
		Dependencies: binding.Dependencies(bindings...),
		ProvideFor:   []string{},
	}
}

func (r *ServiceProvider) Register(app foundation.Application) {
	app.Singleton(contractsbinding.RateLimiter, func(app foundation.Application) (any, error) {
		return NewRateLimiter(), nil
	})
	app.Singleton(contractsbinding.Http, func(app foundation.Application) (any, error) {
		configFacade := app.MakeConfig()
		if configFacade == nil {
			return nil, errors.ConfigFacadeNotSet.SetModule(errors.ModuleHttp)
		}

		j := app.Json()
		if j == nil {
			return nil, errors.JSONParserNotSet.SetModule(errors.ModuleHttp)
		}

		factoryConfig := &client.FactoryConfig{}
		if err := configFacade.UnmarshalKey("http", factoryConfig); err != nil {
			return nil, err
		}

		return client.NewFactory(factoryConfig, configFacade, j, func() contractstelemetry.Telemetry {
			return app.MakeTelemetry()
		})
	})
}

func (r *ServiceProvider) Boot(app foundation.Application) {
	App = app

	app.Commands([]contractsconsole.Command{
		&console.RequestMakeCommand{},
		&console.ControllerMakeCommand{},
		&console.MiddlewareMakeCommand{},
	})
}

func (r *ServiceProvider) Runners(app foundation.Application) []foundation.Runner {
	return []foundation.Runner{NewHTTPRunner(app.MakeConfig(), app.MakeRoute())}
}
