package auth

import (
	contractsauth "github.com/gradii/framework/contracts/auth"
	"github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/contracts/http"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/database"
)

var _ contractsauth.UserProviderFunc = NewOrmUserProvider

type OrmUserProvider struct {
	ctx http.Context
	orm orm.Orm
}

func NewOrmUserProvider(ctx http.Context) (contractsauth.UserProvider, error) {
	if ormFacade == nil {
		return nil, errors.OrmFacadeNotSet.SetModule(errors.ModuleAuth)
	}

	return &OrmUserProvider{
		ctx: ctx,
		orm: ormFacade,
	}, nil
}

// GetID implements auth.UserProvider.
func (r *OrmUserProvider) GetID(user any) (any, error) {
	return database.GetID(user), nil
}

// RetriveByID implements auth.UserProvider.
//
// The id is passed as a bare value, which the ORM resolves against the model's primary key. That is
// the same shape the relation writers use, and it keeps this package free of GORM's clause types.
func (r *OrmUserProvider) RetriveByID(user any, id any) error {
	return r.orm.WithContext(r.ctx).Query().FindOrFail(user, id)
}
