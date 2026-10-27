package main

import (
	"os"

	"github.com/gradii/framework/contracts/facades"
	"github.com/gradii/framework/packages"
	"github.com/gradii/framework/packages/modify"
	"github.com/gradii/framework/support/path"
)

func main() {
	setup := packages.Setup(os.Args)
	stubs := Stubs{}
	notificationFacadePath := path.Facade("notification.go")
	notificationServiceProvider := "&notification.ServiceProvider{}"
	moduleImport := setup.Paths().Module().Import()
	facadesPackage := setup.Paths().Facades().Package()

	setup.Install(
		modify.WhenFacade(facades.Notification,
			modify.RegisterProvider(moduleImport, notificationServiceProvider),
			modify.File(notificationFacadePath).Overwrite(stubs.NotificationFacade(facadesPackage)),
		),
	).Uninstall(
		modify.WhenFacade(facades.Notification,
			modify.UnregisterProvider(moduleImport, notificationServiceProvider),
			modify.File(notificationFacadePath).Remove(),
		),
	).Execute()
}
