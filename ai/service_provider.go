package ai

import (
	"context"

	"github.com/gradii/framework/ai/console"
	contractsai "github.com/gradii/framework/contracts/ai"
	"github.com/gradii/framework/contracts/binding"
	contractsconsole "github.com/gradii/framework/contracts/console"
	contractsfilesystem "github.com/gradii/framework/contracts/filesystem"
	"github.com/gradii/framework/contracts/foundation"
	contractshttpclient "github.com/gradii/framework/contracts/http/client"
)

var (
	aiFacade      contractsai.AI
	storageFacade contractsfilesystem.Storage
	httpFacade    contractshttpclient.Factory
)

type ServiceProvider struct{}

func (r *ServiceProvider) Relationship() binding.Relationship {
	return binding.Relationship{
		Bindings: []string{
			binding.AI,
		},
		Dependencies: binding.Bindings[binding.AI].Dependencies,
	}
}

func (r *ServiceProvider) Register(app foundation.Application) {
	app.Singleton(binding.AI, func(app foundation.Application) (any, error) {
		config := app.MakeConfig()
		var aiConfig contractsai.Config
		if err := config.UnmarshalKey("ai", &aiConfig); err != nil {
			return nil, err
		}

		return NewApplication(context.Background(), aiConfig), nil
	})
}

func (r *ServiceProvider) Boot(app foundation.Application) {
	aiFacade = app.MakeAI()
	storageFacade = app.MakeStorage()
	httpFacade = app.MakeHttp()

	app.Commands([]contractsconsole.Command{
		&console.AgentMakeCommand{},
		&console.ToolMakeCommand{},
	})
}
