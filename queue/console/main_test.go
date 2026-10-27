package console

import (
	"os"
	"testing"

	"github.com/gradii/framework/support/color"
)

func TestMain(m *testing.M) {
	// Console snapshots require ANSI output regardless of the caller's NO_COLOR setting.
	color.Enable()
	os.Exit(m.Run())
}
