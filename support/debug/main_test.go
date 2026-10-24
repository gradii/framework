package debug

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Godump checks NO_COLOR when rendering. Keep ANSI snapshots and caller line numbers stable.
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
