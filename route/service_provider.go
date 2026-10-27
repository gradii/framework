package route

import (
	"github.com/gradii/framework/contracts/binding"
	"github.com/gradii/framework/contracts/console"
	"github.com/gradii/framework/contracts/foundation"
	"github.com/gradii/framework/errors"
	routeconsole "github.com/gradii/framework/route/console"
)

type ServiceProvider struct {
}

func (r *ServiceProvider) Relationship() binding.Relationship {
	return binding.Relationship{
		Bindings: []string{
			binding.Route,
		},
		Dependencies: binding.Bindings[binding.Route].Dependencies,
		ProvideFor:   []string{},
	}
}

func (r *ServiceProvider) Register(app foundation.Application) {
	app.Singleton(binding.Route, func(app foundation.Application) (any, error) {
		config := app.MakeConfig()
		if config == nil {
			return nil, errors.ConfigFacadeNotSet.SetModule(errors.ModuleRoute)
		}

		return NewRoute(config)
	})
}

func (r *ServiceProvider) Boot(app foundation.Application) {
	app.MakeArtisan().Register([]console.Command{
		routeconsole.NewList(app.MakeRoute()),
	})
}
