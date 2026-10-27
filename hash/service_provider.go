package hash

import (
	"github.com/gradii/framework/contracts/binding"
	"github.com/gradii/framework/contracts/foundation"
	"github.com/gradii/framework/errors"
)

type ServiceProvider struct {
}

func (r *ServiceProvider) Relationship() binding.Relationship {
	return binding.Relationship{
		Bindings: []string{
			binding.Hash,
		},
		Dependencies: binding.Bindings[binding.Hash].Dependencies,
		ProvideFor:   []string{},
	}
}

func (r *ServiceProvider) Register(app foundation.Application) {
	app.Singleton(binding.Hash, func(app foundation.Application) (any, error) {
		config := app.MakeConfig()
		if config == nil {
			return nil, errors.ConfigFacadeNotSet.SetModule(errors.ModuleHash)
		}

		return NewApplication(config), nil
	})
}

func (r *ServiceProvider) Boot(foundation.Application) {

}
