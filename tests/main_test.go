package tests

import (
	"os"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/gradii/framework/contracts/binding"
	"github.com/gradii/framework/contracts/foundation"
	"github.com/gradii/framework/database"
	"github.com/gradii/framework/foundation/json"
	mocksfoundation "github.com/gradii/framework/mocks/foundation"
	"github.com/gradii/mysql"
	"github.com/gradii/postgres"
	"github.com/gradii/sqlite"
	"github.com/gradii/sqlserver"
)

func TestMain(m *testing.M) {
	mockApp := &mocksfoundation.Application{}
	mockApp.EXPECT().GetJson().Return(json.New())
	// Register once before opening any pools: registration closes existing connections.
	mockApp.EXPECT().Singleton(mock.Anything, mock.Anything).Run(func(key any, callback func(foundation.Application) (any, error)) {
		if key == binding.Orm {
			ormFactory = callback
		}
	})
	(&database.ServiceProvider{}).Register(mockApp)
	postgres.App = mockApp
	mysql.App = mockApp
	sqlite.App = mockApp
	sqlserver.App = mockApp

	code := m.Run()

	// The containers outlive the test binary, so the databases this run created
	// are dropped once the tests are over; without that every run leaves about
	// a hundred behind and the container's catalog grows without bound.
	dropCreatedDatabases()

	os.Exit(code)
}
