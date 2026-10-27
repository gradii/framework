package view

import (
	contractsbinding "github.com/gradii/framework/contracts/binding"
	contractsconsole "github.com/gradii/framework/contracts/console"
	"github.com/gradii/framework/contracts/foundation"
	"github.com/gradii/framework/support/binding"
	"github.com/gradii/framework/view/console"
)

type ServiceProvider struct{}

func (r *ServiceProvider) Relationship() contractsbinding.Relationship {
	bindings := []string{
		contractsbinding.View,
	}

	return contractsbinding.Relationship{
		Bindings:     bindings,
		Dependencies: binding.Dependencies(bindings...),
		ProvideFor:   []string{},
	}
}

func (r *ServiceProvider) Register(app foundation.Application) {
	app.Singleton(contractsbinding.View, func(app foundation.Application) (any, error) {
		return NewView(), nil
	})
}

func (r *ServiceProvider) Boot(app foundation.Application) {
	r.registerCommands(app)
}

func (r *ServiceProvider) registerCommands(app foundation.Application) {
	app.Commands([]contractsconsole.Command{
		console.NewViewMakeCommand(app.MakeConfig()),
	})
}
