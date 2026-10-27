package testing

import (
	"github.com/gradii/framework/contracts/binding"
	contractsconsole "github.com/gradii/framework/contracts/console"
	"github.com/gradii/framework/contracts/foundation"
	contractsroute "github.com/gradii/framework/contracts/route"
	contractsession "github.com/gradii/framework/contracts/session"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/color"
)

var (
	json          foundation.Json
	artisanFacade contractsconsole.Artisan
	routeFacade   contractsroute.Route
	sessionFacade contractsession.Manager
)

type ServiceProvider struct {
}

func (r *ServiceProvider) Relationship() binding.Relationship {
	return binding.Relationship{
		Bindings: []string{
			binding.Testing,
		},
		Dependencies: binding.Bindings[binding.Testing].Dependencies,
		ProvideFor:   []string{},
	}
}

func (r *ServiceProvider) Register(app foundation.Application) {
	app.Singleton(binding.Testing, func(app foundation.Application) (any, error) {
		config := app.MakeConfig()
		if config == nil {
			return nil, errors.ConfigFacadeNotSet.SetModule(errors.ModuleTesting)
		}

		// They are checked when using actually
		artisan := app.MakeArtisan()
		cache := app.MakeCache()
		orm := app.MakeOrm()
		process := app.MakeProcess()

		return NewApplication(artisan, cache, config, orm, process), nil
	})
}

func (r *ServiceProvider) Boot(app foundation.Application) {
	artisanFacade = app.MakeArtisan()
	if artisanFacade == nil {
		color.Errorln(errors.ConsoleFacadeNotSet.SetModule(errors.ModuleTesting))
	}

	routeFacade = app.MakeRoute()
	if routeFacade == nil {
		color.Errorln(errors.RouteFacadeNotSet.SetModule(errors.ModuleTesting))
	}

	sessionFacade = app.MakeSession()
	if sessionFacade == nil {
		color.Errorln(errors.SessionFacadeNotSet.SetModule(errors.ModuleTesting))
	}

	json = app.GetJson()
}
