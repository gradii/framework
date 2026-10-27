package config

import (
	"github.com/gradii/framework/contracts/binding"
	"github.com/gradii/framework/contracts/foundation"
	"github.com/gradii/framework/support"
)

type ServiceProvider struct {
}

func (r *ServiceProvider) Relationship() binding.Relationship {
	return binding.Relationship{
		Bindings: []string{
			binding.Config,
		},
		Dependencies: []string{},
		ProvideFor:   []string{},
	}
}

func (r *ServiceProvider) Register(app foundation.Application) {
	app.Singleton(binding.Config, func(app foundation.Application) (any, error) {
		return NewApplication(support.EnvFilePath), nil
	})
}

func (r *ServiceProvider) Boot(app foundation.Application) {

}
