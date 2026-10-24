package color

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// These tests assert ANSI sequences, so color detection must not depend on the shell.
	Enable()
	os.Exit(m.Run())
}
