package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// laravelCase and runLaravelCases carry the corpus cases that need no dialect: they run
// against the base grammar, which quotes with double quotes and binds with "?".
type laravelCase struct {
	name  string
	build func(*Builder) *Builder
	want  string
}

func runLaravelCases(t *testing.T, cases []laravelCase) {
	t.Helper()

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.build(newTestBuilder()).ToSQL())
		})
	}
}
