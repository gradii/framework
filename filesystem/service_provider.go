package filesystem

import (
	"github.com/gradii/framework/contracts/binding"
	configcontract "github.com/gradii/framework/contracts/config"
	filesystemcontract "github.com/gradii/framework/contracts/filesystem"
	"github.com/gradii/framework/contracts/foundation"
	"github.com/gradii/framework/errors"
)

var (
	ConfigFacade  configcontract.Config
	StorageFacade filesystemcontract.Storage
)

type ServiceProvider struct {
}

func (r *ServiceProvider) Relationship() binding.Relationship {
	return binding.Relationship{
		Bindings: []string{
			binding.Storage,
		},
		Dependencies: binding.Bindings[binding.Storage].Dependencies,
		ProvideFor:   []string{},
	}
}

func (r *ServiceProvider) Register(app foundation.Application) {
	app.Singleton(binding.Storage, func(app foundation.Application) (any, error) {
		config := app.MakeConfig()
		if config == nil {
			return nil, errors.ConfigFacadeNotSet.SetModule(errors.ModuleFilesystem)
		}

		return NewStorage(config)
	})
}

func (r *ServiceProvider) Boot(app foundation.Application) {
	ConfigFacade = app.MakeConfig()
	StorageFacade = app.MakeStorage()
}
