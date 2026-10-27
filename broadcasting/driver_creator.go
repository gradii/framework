package broadcasting

import (
	"github.com/gradii/framework/broadcasting/broadcasters"
	"github.com/gradii/framework/contracts/broadcasting"
	"github.com/gradii/framework/contracts/foundation"
	"github.com/gradii/framework/errors"
)

func CreateDriver(conn broadcasting.ConnectionConfig, app foundation.Application) (broadcasting.Driver, error) {
	switch conn.Driver {
	case "pusher":
		httpClient := app.MakeHttp()
		if httpClient == nil {
			return nil, errors.HttpFacadeNotSet.SetModule(errors.ModuleBroadcast)
		}
		return broadcasters.NewPusherDriver(conn, httpClient)
	case "log":
		logFacade := app.MakeLog()
		if logFacade == nil {
			return nil, errors.LogFacadeNotSet.SetModule(errors.ModuleBroadcast)
		}
		return broadcasters.NewLogDriver(logFacade), nil
	case "null":
		return broadcasters.NewNullDriver(), nil
	default:
		return nil, errors.BroadcastDriverNotSupported.Args(conn.Driver)
	}
}
