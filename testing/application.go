package testing

import (
	contractscache "github.com/gradii/framework/contracts/cache"
	contractsconfig "github.com/gradii/framework/contracts/config"
	contractsconsole "github.com/gradii/framework/contracts/console"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	contractsprocess "github.com/gradii/framework/contracts/process"
	"github.com/gradii/framework/contracts/testing"
	"github.com/gradii/framework/testing/docker"
)

type Application struct {
	artisan contractsconsole.Artisan
	cache   contractscache.Cache
	config  contractsconfig.Config
	orm     contractsorm.Orm
	process contractsprocess.Process
}

func NewApplication(
	artisan contractsconsole.Artisan,
	cache contractscache.Cache,
	config contractsconfig.Config,
	orm contractsorm.Orm,
	process contractsprocess.Process,
) *Application {
	return &Application{
		artisan: artisan,
		cache:   cache,
		config:  config,
		orm:     orm,
		process: process,
	}
}

func (r *Application) Docker() testing.Docker {
	return docker.NewDocker(r.artisan, r.cache, r.config, r.orm, r.process)
}
