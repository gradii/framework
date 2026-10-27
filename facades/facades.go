package facades

import (
	"context"

	"github.com/gradii/framework/contracts/ai"
	"github.com/gradii/framework/contracts/auth"
	"github.com/gradii/framework/contracts/auth/access"
	"github.com/gradii/framework/contracts/broadcasting"
	"github.com/gradii/framework/contracts/cache"
	"github.com/gradii/framework/contracts/config"
	"github.com/gradii/framework/contracts/console"
	"github.com/gradii/framework/contracts/crypt"
	"github.com/gradii/framework/contracts/database/db"
	"github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/contracts/database/schema"
	"github.com/gradii/framework/contracts/database/seeder"
	"github.com/gradii/framework/contracts/event"
	"github.com/gradii/framework/contracts/filesystem"
	foundationcontract "github.com/gradii/framework/contracts/foundation"
	"github.com/gradii/framework/contracts/grpc"
	"github.com/gradii/framework/contracts/hash"
	"github.com/gradii/framework/contracts/http"
	"github.com/gradii/framework/contracts/http/client"
	"github.com/gradii/framework/contracts/log"
	"github.com/gradii/framework/contracts/mail"
	"github.com/gradii/framework/contracts/notification"
	"github.com/gradii/framework/contracts/process"
	"github.com/gradii/framework/contracts/queue"
	"github.com/gradii/framework/contracts/route"
	"github.com/gradii/framework/contracts/schedule"
	"github.com/gradii/framework/contracts/session"
	"github.com/gradii/framework/contracts/telemetry"
	"github.com/gradii/framework/contracts/testing"
	"github.com/gradii/framework/contracts/translation"
	"github.com/gradii/framework/contracts/validation"
	"github.com/gradii/framework/contracts/view"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/foundation"
)

func App() foundationcontract.Application {
	if foundation.App == nil {
		panic(errors.ApplicationNotSet.SetModule(errors.ModuleFacade))
	} else {
		return foundation.App
	}
}

func AI() ai.AI {
	return App().MakeAI()
}

func Artisan() console.Artisan {
	return App().MakeArtisan()
}

func Auth(ctx ...http.Context) auth.Auth {
	return App().MakeAuth(ctx...)
}

func Broadcast() broadcasting.Broadcast {
	return App().MakeBroadcast()
}

func Cache() cache.Cache {
	return App().MakeCache()
}

func Config() config.Config {
	return App().MakeConfig()
}

func Crypt() crypt.Crypt {
	return App().MakeCrypt()
}

func DB() db.DB {
	return App().MakeDB()
}

func Event() event.Instance {
	return App().MakeEvent()
}

func Gate() access.Gate {
	return App().MakeGate()
}

func Grpc() grpc.Grpc {
	return App().MakeGrpc()
}

func Hash() hash.Hash {
	return App().MakeHash()
}

func Http() client.Factory {
	return App().MakeHttp()
}

func Lang(ctx context.Context) translation.Translator {
	return App().MakeLang(ctx)
}

func Log() log.Log {
	return App().MakeLog()
}

func Mail() mail.Mail {
	return App().MakeMail()
}

func Notification() notification.Manager {
	return App().MakeNotification()
}

func Orm() orm.Orm {
	return App().MakeOrm()
}

func Process() process.Process {
	return App().MakeProcess()
}

func Queue() queue.Queue {
	return App().MakeQueue()
}

func RateLimiter() http.RateLimiter {
	return App().MakeRateLimiter()
}

func Route() route.Route {
	return App().MakeRoute()
}

func Schedule() schedule.Schedule {
	return App().MakeSchedule()
}

func Schema() schema.Schema {
	return App().MakeSchema()
}

func Seeder() seeder.Facade {
	return App().MakeSeeder()
}

func Session() session.Manager {
	return App().MakeSession()
}

func Storage() filesystem.Storage {
	return App().MakeStorage()
}

func Telemetry() telemetry.Telemetry {
	return App().MakeTelemetry()
}

func Testing() testing.Testing {
	return App().MakeTesting()
}

func Validation() validation.Validation {
	return App().MakeValidation()
}

func View() view.View {
	return App().MakeView()
}
