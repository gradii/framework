package queue

import (
	contractscache "github.com/gradii/framework/contracts/cache"
	contractsdb "github.com/gradii/framework/contracts/database/db"
	contractsfoundation "github.com/gradii/framework/contracts/foundation"
	contractslog "github.com/gradii/framework/contracts/log"
	contractsqueue "github.com/gradii/framework/contracts/queue"
	"github.com/gradii/framework/errors"
)

type DriverCreator struct {
	config    contractsqueue.Config
	cache     contractscache.Cache
	db        contractsdb.DB
	jobStorer contractsqueue.JobStorer
	json      contractsfoundation.Json
	log       contractslog.Log
}

func NewDriverCreator(config contractsqueue.Config, cache contractscache.Cache, db contractsdb.DB, jobStorer contractsqueue.JobStorer, json contractsfoundation.Json, log contractslog.Log) *DriverCreator {
	return &DriverCreator{
		config:    config,
		cache:     cache,
		db:        db,
		jobStorer: jobStorer,
		json:      json,
		log:       log,
	}
}

func (r *DriverCreator) Create(connection string) (contractsqueue.Driver, error) {
	driver := r.config.Driver(connection)

	switch driver {
	case contractsqueue.DriverSync:
		return NewSync(), nil
	case contractsqueue.DriverDatabase:
		if r.db == nil {
			return nil, errors.QueueInvalidDatabaseConnection.Args(connection)
		}

		return NewDatabase(r.config, r.cache, r.db, r.jobStorer, r.json, connection)
	case contractsqueue.DriverCustom:
		custom := r.config.Via(connection)
		if driver, ok := custom.(contractsqueue.Driver); ok {
			return driver, nil
		}
		if driver, ok := custom.(func() (contractsqueue.Driver, error)); ok {
			return driver()
		}
		return nil, errors.QueueDriverInvalid.Args(connection)
	default:
		return nil, errors.QueueDriverNotSupported.Args(driver)
	}
}
