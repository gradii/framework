package grammars

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWrap_AliasedValueMatchesTheOracle pins Grammar::wrap()'s aliased-value branch, the path the
// keyed pluck relies on for an aliased key column. Oracle (illuminate/database v12.55.1, executed):
// wrapAliasedValue splits the value on /\s+as\s+/i and wraps the first two segments with the
// connector re-emitted as " as " — the split consumes the whole whitespace run, and anything past a
// second connector is dropped (preg_split's third segment is never wrapped). The stripos(' as ')
// gate keeps a tab-separated "as" on the plain segment path.
func TestWrap_AliasedValueMatchesTheOracle(t *testing.T) {
	g := NewBaseGrammar("")
	g.Attach(g)

	cases := []struct {
		in   string
		want string
	}{
		{"users.email as user_email", `"users"."email" as "user_email"`},
		{"users.email AS user_email", `"users"."email" as "user_email"`},
		{"users.email  as  user_email", `"users"."email" as "user_email"`},
		{"a as b as c", `"a" as "b"`},
		{"x as y as z as w", `"x" as "y"`},
		{"users.email\tas\tuser_email", "\"users\".\"email\tas\tuser_email\""},
	}

	for _, c := range cases {
		assert.Equal(t, c.want, g.Wrap(c.in), c.in)
	}
}
