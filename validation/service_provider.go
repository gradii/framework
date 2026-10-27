package validation

import (
	"context"

	"github.com/gradii/framework/contracts/binding"
	consolecontract "github.com/gradii/framework/contracts/console"
	"github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/contracts/foundation"
	contractstranslation "github.com/gradii/framework/contracts/translation"
	"github.com/gradii/framework/validation/console"
)

var (
	ormFacade  orm.Orm
	langFacade = func(ctx context.Context) contractstranslation.Translator { return nil }
)

type ServiceProvider struct {
}

func (r *ServiceProvider) Relationship() binding.Relationship {
	return binding.Relationship{
		Bindings: []string{
			binding.Validation,
		},
		Dependencies: binding.Bindings[binding.Validation].Dependencies,
		ProvideFor:   []string{},
	}
}

func (r *ServiceProvider) Register(app foundation.Application) {
	app.Singleton(binding.Validation, func(app foundation.Application) (any, error) {
		return NewValidation(), nil
	})
}

func (r *ServiceProvider) Boot(app foundation.Application) {
	ormFacade = app.MakeOrm()
	langFacade = app.MakeLang

	app.Commands([]consolecontract.Command{
		console.NewLangPublishCommand(app.LangPath(), LangFS),
		&console.RuleMakeCommand{},
		&console.FilterMakeCommand{},
	})
}
